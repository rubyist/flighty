package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const aeroAPIBase = "https://aeroapi.flightaware.com/aeroapi"

type config struct {
	AeroAPIKey     string  `json:"aeroApiKey"`
	HomeAirport    string  `json:"homeAirport"`
	OverheadLatMin float64 `json:"overheadLatMin"`
	OverheadLatMax float64 `json:"overheadLatMax"`
	OverheadLonMin float64 `json:"overheadLonMin"`
	OverheadLonMax float64 `json:"overheadLonMax"`
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
	FaFlightID   string      `json:"fa_flight_id"`
	Origin       aeroAirport `json:"origin"`
	Destination  aeroAirport `json:"destination"`
	EstimatedOff string      `json:"estimated_off"`
	EstimatedOn  string      `json:"estimated_on"`
	ScheduledOff string      `json:"scheduled_off"`
	ScheduledOn  string      `json:"scheduled_on"`
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
	FaFlightID   string          `json:"faFlightId"`
	Origin       frontendAirport `json:"origin"`
	Destination  frontendAirport `json:"destination"`
	Time         string          `json:"time"`
	AircraftType string          `json:"aircraftType"`
	Registration string          `json:"registration"`
	Status           string `json:"status"`
	StatusTime       string `json:"statusTime"`
	TimeSource       string `json:"timeSource"`
	StatusTimeSource string `json:"statusTimeSource"`
	ScheduledOff string          `json:"scheduledOff,omitempty"`
	EstimatedOff string          `json:"estimatedOff,omitempty"`
	ActualOff    string          `json:"actualOff,omitempty"`
	ScheduledOn  string          `json:"scheduledOn,omitempty"`
	EstimatedOn  string          `json:"estimatedOn,omitempty"`
	ActualOn     string          `json:"actualOn,omitempty"`
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

type labeledVal struct {
	val, label string
}

func firstNonEmptyWithSource(pairs ...labeledVal) (string, string) {
	for _, p := range pairs {
		if p.val != "" {
			return p.val, p.label
		}
	}
	return "", ""
}

func toFrontendAirport(a aeroAirport) frontendAirport {
	return frontendAirport{Code: a.Code, Name: a.Name}
}

func toFrontendDeparture(f aeroFlight) frontendFlight {
	timeVal, timeSource := firstNonEmptyWithSource(
		labeledVal{f.ActualOff, "A"},
		labeledVal{f.EstimatedOff, "E"},
		labeledVal{f.ScheduledOff, "S"},
	)

	var statusTime, statusTimeSource string
	if strings.Contains(strings.ToLower(f.Status), "arrived") {
		statusTime = f.ActualOn
		if statusTime != "" {
			statusTimeSource = "A"
		}
	} else if strings.Contains(strings.ToLower(f.Status), "en route") {
		statusTime, statusTimeSource = firstNonEmptyWithSource(
			labeledVal{f.EstimatedOn, "E"},
			labeledVal{f.ScheduledOn, "S"},
		)
	} else {
		statusTime = f.ScheduledOff
		if statusTime != "" {
			statusTimeSource = "S"
		}
	}
	return frontendFlight{
		Ident:            f.Ident,
		FaFlightID:       f.FaFlightID,
		Origin:           toFrontendAirport(f.Origin),
		Destination:      toFrontendAirport(f.Destination),
		Time:             timeVal,
		TimeSource:       timeSource,
		AircraftType:     f.AircraftType,
		Registration:     f.Registration,
		Status:           f.Status,
		StatusTime:       statusTime,
		StatusTimeSource: statusTimeSource,
		ScheduledOff:     f.ScheduledOff,
		EstimatedOff:     f.EstimatedOff,
		ActualOff:        f.ActualOff,
		ScheduledOn:      f.ScheduledOn,
		EstimatedOn:      f.EstimatedOn,
		ActualOn:         f.ActualOn,
	}
}

func toFrontendArrival(f aeroFlight) frontendFlight {
	timeVal, timeSource := firstNonEmptyWithSource(
		labeledVal{f.ActualOn, "A"},
		labeledVal{f.EstimatedOn, "E"},
		labeledVal{f.ScheduledOn, "S"},
	)

	var statusTime, statusTimeSource string
	if strings.Contains(strings.ToLower(f.Status), "arrived") {
		statusTime = f.ActualOn
		if statusTime != "" {
			statusTimeSource = "A"
		}
	} else if strings.Contains(strings.ToLower(f.Status), "en route") {
		statusTime, statusTimeSource = firstNonEmptyWithSource(
			labeledVal{f.EstimatedOn, "E"},
			labeledVal{f.ScheduledOn, "S"},
		)
	} else {
		statusTime = f.ScheduledOff
		if statusTime != "" {
			statusTimeSource = "S"
		}
	}
	return frontendFlight{
		Ident:            f.Ident,
		FaFlightID:       f.FaFlightID,
		Origin:           toFrontendAirport(f.Origin),
		Destination:      toFrontendAirport(f.Destination),
		Time:             timeVal,
		TimeSource:       timeSource,
		AircraftType:     f.AircraftType,
		Registration:     f.Registration,
		Status:           f.Status,
		StatusTime:       statusTime,
		StatusTimeSource: statusTimeSource,
		ScheduledOff:     f.ScheduledOff,
		EstimatedOff:     f.EstimatedOff,
		ActualOff:        f.ActualOff,
		ScheduledOn:      f.ScheduledOn,
		EstimatedOn:      f.EstimatedOn,
		ActualOn:         f.ActualOn,
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
		thirtyMinAgo := now.Add(-30 * time.Minute)
		endOfDay := now.Truncate(24 * time.Hour).Add(24 * time.Hour)

		start := r.URL.Query().Get("start")
		end := r.URL.Query().Get("end")
		if start == "" {
			start = thirtyMinAgo.UTC().Format(time.RFC3339)
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

func makeAirportHandler(apiKey string, cache *airportCache) http.HandlerFunc {
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

		// Fetch from AeroAPI
		apiURL := fmt.Sprintf("%s/airports/%s", aeroAPIBase, code)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
		if err != nil {
			http.Error(w, "failed to create request", http.StatusInternalServerError)
			return
		}
		req.Header.Set("x-apikey", apiKey)

		resp, err := http.DefaultClient.Do(req)
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

		// If AeroAPI had no name, derive one from the SkyVector URL slug
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

func makeFlightMapHandler(apiKey string) http.HandlerFunc {
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

		flightID := r.URL.Query().Get("id")
		if flightID == "" {
			http.Error(w, "missing id parameter", http.StatusBadRequest)
			return
		}

		apiURL := fmt.Sprintf("%s/flights/%s/map?show_airports=true", aeroAPIBase, flightID)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
		if err != nil {
			http.Error(w, "failed to create request", http.StatusInternalServerError)
			return
		}
		req.Header.Set("x-apikey", apiKey)

		log.Printf("Fetching flight map: %s", apiURL)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Error fetching flight map: %v", err)
			http.Error(w, "failed to fetch map", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "failed to read response", http.StatusBadGateway)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("AeroAPI map error (%d): %s", resp.StatusCode, body)
			http.Error(w, fmt.Sprintf("AeroAPI error: %d", resp.StatusCode), resp.StatusCode)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

// Overhead flight search: returns fa_flight_ids of flights within the configured lat/lon box

type positionEntry struct {
	FaFlightID string `json:"fa_flight_id"`
}

type positionsResponse struct {
	Positions []positionEntry `json:"positions"`
}

func makeOverheadHandler(apiKey string, cfg config) http.HandlerFunc {
	var mu sync.Mutex
	cachedIds := make(map[string]bool)
	var lastQueryTime time.Time

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

		if cfg.OverheadLatMin == 0 && cfg.OverheadLatMax == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"flightIds":[]}`))
			return
		}

		now := time.Now()
		oneHourAgo := now.Add(-1 * time.Hour)

		// Use the later of lastQueryTime or oneHourAgo as the start
		mu.Lock()
		startTime := oneHourAgo
		if !lastQueryTime.IsZero() && lastQueryTime.After(oneHourAgo) {
			startTime = lastQueryTime
		}
		mu.Unlock()

		query := fmt.Sprintf("{range lat %f %f} {range lon %f %f} {range clock %d %d}",
			cfg.OverheadLatMin, cfg.OverheadLatMax,
			cfg.OverheadLonMin, cfg.OverheadLonMax,
			startTime.Unix(), now.Unix())

		apiURL := fmt.Sprintf("%s/flights/search/positions?unique_flights=true&query=%s",
			aeroAPIBase, url.QueryEscape(query))

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
		if err != nil {
			http.Error(w, "failed to create request", http.StatusInternalServerError)
			return
		}
		req.Header.Set("x-apikey", apiKey)

		log.Printf("Fetching overhead flights: %s", apiURL)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Error fetching overhead flights: %v", err)
			http.Error(w, "failed to fetch overhead data", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "failed to read response", http.StatusBadGateway)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("AeroAPI positions error (%d): %s", resp.StatusCode, body)
			http.Error(w, fmt.Sprintf("AeroAPI error: %d", resp.StatusCode), resp.StatusCode)
			return
		}

		var posResp positionsResponse
		if err := json.Unmarshal(body, &posResp); err != nil {
			log.Printf("Error parsing positions response: %v", err)
			http.Error(w, "failed to parse position data", http.StatusInternalServerError)
			return
		}

		mu.Lock()
		lastQueryTime = now
		// Merge new results into cache, and evict IDs not seen in the last hour
		for _, p := range posResp.Positions {
			if p.FaFlightID != "" {
				cachedIds[p.FaFlightID] = true
			}
		}
		// Build response from full cache
		ids := make([]string, 0, len(cachedIds))
		for id := range cachedIds {
			ids = append(ids, id)
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]string{"flightIds": ids})
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
	http.HandleFunc("/flight", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "flight.html")
	})
	http.HandleFunc("/flight/map", makeFlightMapHandler(cfg.AeroAPIKey))
	http.HandleFunc("/flights", makeFlightsHandler(cfg.AeroAPIKey, cfg.HomeAirport))
	http.HandleFunc("/overhead", makeOverheadHandler(cfg.AeroAPIKey, cfg))
	http.HandleFunc("/airport", makeAirportHandler(cfg.AeroAPIKey, ac))

	log.Println("Server listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
