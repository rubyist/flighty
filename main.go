package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	aeroAPIBase  = "https://aeroapi.flightaware.com/aeroapi"
	airportDBAPI = "https://airportdb.io/api/v1/airport"
)

type config struct {
	AeroAPIKey     string `json:"aeroApiKey"`
	AirportDBToken string `json:"airportDbToken"`
	HomeAirport    string `json:"homeAirport"`
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("reading config file: %w", err)
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.AeroAPIKey == "" {
		return config{}, fmt.Errorf("aeroApiKey must be set in config file")
	}
	if cfg.AirportDBToken == "" {
		return config{}, fmt.Errorf("airportDbToken must be set in config file")
	}
	if cfg.HomeAirport == "" {
		return config{}, fmt.Errorf("homeAirport must be set in config file")
	}
	return cfg, nil
}

// AeroAPI response types

type aeroAirport struct {
	Code           string `json:"code"`
	Name           string `json:"name"`
	AirportInfoURL string `json:"airport_info_url"`
}

type aeroFlight struct {
	Ident        string      `json:"ident"`
	Origin       aeroAirport `json:"origin"`
	Destination  aeroAirport `json:"destination"`
	ScheduledOut string      `json:"scheduled_out"`
	ScheduledOff string      `json:"scheduled_off"`
	ScheduledIn  string      `json:"scheduled_in"`
	ScheduledOn  string      `json:"scheduled_on"`
	ActualOut    string      `json:"actual_out"`
	ActualIn     string      `json:"actual_in"`
	ActualOff    string      `json:"actual_off"`
	ActualOn     string      `json:"actual_on"`
	AircraftType string      `json:"aircraft_type"`
	Registration string      `json:"registration"`
	Status       string      `json:"status"`
}

type aeroFlightsResponse struct {
	Departures          []aeroFlight `json:"departures"`
	Arrivals            []aeroFlight `json:"arrivals"`
	ScheduledDepartures []aeroFlight `json:"scheduled_departures"`
	ScheduledArrivals   []aeroFlight `json:"scheduled_arrivals"`
	Links               interface{}  `json:"links"`
	NumPages            int          `json:"num_pages"`
}

// Simplified flight for the frontend

type frontendAirport struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type frontendFlight struct {
	Ident        string          `json:"ident"`
	Origin       frontendAirport `json:"origin"`
	Destination  frontendAirport `json:"destination"`
	Time         string          `json:"time"`
	AircraftType string          `json:"aircraftType"`
	Registration string          `json:"registration"`
	Status       string          `json:"status"`
}

type frontendResponse struct {
	Departures []frontendFlight `json:"departures"`
	Arrivals   []frontendFlight `json:"arrivals"`
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func toFrontendAirport(a aeroAirport) frontendAirport {
	return frontendAirport{Code: a.Code, Name: a.Name}
}

func toFrontendDeparture(f aeroFlight) frontendFlight {
	return frontendFlight{
		Ident:        f.Ident,
		Origin:       toFrontendAirport(f.Origin),
		Destination:  toFrontendAirport(f.Destination),
		Time:         firstNonEmpty(f.ActualOff, f.ScheduledOff, f.ScheduledOut, f.ActualOut),
		AircraftType: f.AircraftType,
		Registration: f.Registration,
		Status:       f.Status,
	}
}

func toFrontendArrival(f aeroFlight) frontendFlight {
	return frontendFlight{
		Ident:        f.Ident,
		Origin:       toFrontendAirport(f.Origin),
		Destination:  toFrontendAirport(f.Destination),
		Time:         firstNonEmpty(f.ActualOn, f.ScheduledOn, f.ScheduledIn, f.ActualIn),
		AircraftType: f.AircraftType,
		Registration: f.Registration,
		Status:       f.Status,
	}
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func makeFlightsHandler(apiKey string, airport string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		now := time.Now()
		twoHoursAgo := now.Add(-2 * time.Hour)
		endOfDay := now.Truncate(24 * time.Hour).Add(24 * time.Hour)

		start := r.URL.Query().Get("start")
		end := r.URL.Query().Get("end")
		if start == "" {
			start = twoHoursAgo.UTC().Format(time.RFC3339)
		}
		if end == "" {
			end = endOfDay.UTC().Format(time.RFC3339)
		}

		apiURL := fmt.Sprintf("%s/airports/%s/flights?start=%s&end=%s",
			aeroAPIBase, airport, start, end)

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
		if err != nil {
			http.Error(w, "failed to create request", http.StatusInternalServerError)
			return
		}
		req.Header.Set("x-apikey", apiKey)

		log.Printf("Fetching flights: %s", apiURL)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Error fetching flights: %v", err)
			http.Error(w, "failed to fetch flight data", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "failed to read response", http.StatusBadGateway)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("AeroAPI error (%d): %s", resp.StatusCode, body)
			http.Error(w, fmt.Sprintf("AeroAPI error: %d", resp.StatusCode), resp.StatusCode)
			return
		}

		var aeroResp aeroFlightsResponse
		if err := json.Unmarshal(body, &aeroResp); err != nil {
			log.Printf("Error parsing AeroAPI response: %v", err)
			http.Error(w, "failed to parse flight data", http.StatusInternalServerError)
			return
		}

		// Combine scheduled + actual flights for each direction, deduplicating by ident+time
		var departures []frontendFlight
		seen := make(map[string]bool)
		for _, list := range [][]aeroFlight{aeroResp.Departures, aeroResp.ScheduledDepartures} {
			for _, f := range list {
				ff := toFrontendDeparture(f)
				key := ff.Ident + "|" + ff.Time
				if !seen[key] {
					seen[key] = true
					departures = append(departures, ff)
				}
			}
		}

		var arrivals []frontendFlight
		seen = make(map[string]bool)
		for _, list := range [][]aeroFlight{aeroResp.Arrivals, aeroResp.ScheduledArrivals} {
			for _, f := range list {
				ff := toFrontendArrival(f)
				key := ff.Ident + "|" + ff.Time
				if !seen[key] {
					seen[key] = true
					arrivals = append(arrivals, ff)
				}
			}
		}

		result := frontendResponse{
			Departures: departures,
			Arrivals:   arrivals,
		}
		if result.Departures == nil {
			result.Departures = []frontendFlight{}
		}
		if result.Arrivals == nil {
			result.Arrivals = []frontendFlight{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// Airport lookup (unchanged)

const (
	airportCacheFile = "airport-cache.json"
	skyVectorAPI     = "https://skyvector.com/api/airportSearch"
)

type airportInfo struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type airportCache struct {
	mu       sync.RWMutex
	airports map[string]airportInfo
}

func loadAirportCache() *airportCache {
	ac := &airportCache{airports: make(map[string]airportInfo)}
	data, err := os.ReadFile(airportCacheFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: could not read airport cache: %v", err)
		}
		return ac
	}
	if err := json.Unmarshal(data, &ac.airports); err != nil {
		log.Printf("Warning: could not parse airport cache: %v", err)
		return ac
	}
	log.Printf("Loaded %d airports from cache", len(ac.airports))
	return ac
}

func (ac *airportCache) save() {
	ac.mu.RLock()
	data, err := json.MarshalIndent(ac.airports, "", "  ")
	ac.mu.RUnlock()
	if err != nil {
		log.Printf("Warning: could not marshal airport cache: %v", err)
		return
	}
	if err := os.WriteFile(airportCacheFile, data, 0644); err != nil {
		log.Printf("Warning: could not write airport cache: %v", err)
	}
}

func fetchSkyVectorURL(code string) string {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(fmt.Sprintf("%s?query=%s", skyVectorAPI, code))
	if err != nil {
		log.Printf("Warning: SkyVector lookup failed for %s: %v", code, err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusMovedPermanently {
		loc := resp.Header.Get("Location")
		if loc != "" && !strings.HasPrefix(loc, "http") {
			loc = "https://skyvector.com" + loc
		}
		return loc
	}
	return ""
}

var airportCodePattern = regexp.MustCompile(`^[A-Z0-9]{3,4}$`)

func makeAirportHandler(apiToken string, cache *airportCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("code")))
		if !airportCodePattern.MatchString(code) {
			http.Error(w, "invalid airport code", http.StatusBadRequest)
			return
		}

		// Check cache first
		cache.mu.RLock()
		info, ok := cache.airports[code]
		cache.mu.RUnlock()
		if ok {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(info)
			return
		}

		// Fetch from AirportDB
		apiURL := fmt.Sprintf("%s/%s?apiToken=%s", airportDBAPI, code, apiToken)
		resp, err := http.Get(apiURL)
		if err != nil {
			log.Printf("Error fetching airport %s: %v", code, err)
			http.Error(w, "failed to fetch airport data", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		info = airportInfo{}
		if resp.StatusCode == http.StatusOK {
			var result struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				log.Printf("Error decoding airport %s: %v", code, err)
			} else {
				info.Name = result.Name
			}
		}

		// Fetch SkyVector URL
		info.URL = fetchSkyVectorURL(code)

		// If AirportDB had no name, derive one from the SkyVector URL slug
		if info.Name == "" && info.URL != "" {
			parts := strings.Split(info.URL, "/")
			if len(parts) > 0 {
				slug := parts[len(parts)-1]
				info.Name = strings.ReplaceAll(slug, "-", " ")
			}
		}

		// Cache the result and persist to disk
		cache.mu.Lock()
		cache.airports[code] = info
		cache.mu.Unlock()
		cache.save()

		log.Printf("Cached new airport: %s = %+v", code, info)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	}
}

func main() {
	cfg, err := loadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	log.Printf("Loaded config for home airport: %s", cfg.HomeAirport)

	ac := loadAirportCache()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})
	http.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"homeAirport": cfg.HomeAirport})
	})
	http.HandleFunc("/flights", makeFlightsHandler(cfg.AeroAPIKey, cfg.HomeAirport))
	http.HandleFunc("/airport", makeAirportHandler(cfg.AirportDBToken, ac))

	log.Println("Server listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
