package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	tokenURL   = "https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token"
	openskyAPI = "https://opensky-network.org/api/flights"
	airport    = "KPDK"
)

type credentials struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type tokenCache struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
	creds     credentials
}

func loadCredentials(path string) (credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, fmt.Errorf("reading credentials file: %w", err)
	}
	var creds credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return credentials{}, fmt.Errorf("parsing credentials: %w", err)
	}
	if creds.ClientID == "" || creds.ClientSecret == "" {
		return credentials{}, fmt.Errorf("clientId and clientSecret must be set in credentials file")
	}
	return creds, nil
}

func (tc *tokenCache) getToken() (string, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.token != "" && time.Now().Before(tc.expiresAt) {
		return tc.token, nil
	}

	resp, err := http.PostForm(tokenURL, url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {tc.creds.ClientID},
		"client_secret": {tc.creds.ClientSecret},
	})
	if err != nil {
		return "", fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed (%d): %s", resp.StatusCode, body)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	tc.token = tr.AccessToken
	// Refresh 60 seconds before expiry to avoid edge cases
	tc.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn-60) * time.Second)

	log.Printf("Obtained new access token (expires in %ds)", tr.ExpiresIn)
	return tc.token, nil
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func makeFlightHandler(tc *tokenCache, flightType string) http.HandlerFunc {
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
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		defaultBegin := startOfDay.Unix()
		defaultEnd := now.Unix()

		begin := defaultBegin
		end := defaultEnd

		if b := r.URL.Query().Get("begin"); b != "" {
			parsed, err := strconv.ParseInt(b, 10, 64)
			if err != nil {
				http.Error(w, "invalid begin parameter", http.StatusBadRequest)
				return
			}
			begin = parsed
		}
		if e := r.URL.Query().Get("end"); e != "" {
			parsed, err := strconv.ParseInt(e, 10, 64)
			if err != nil {
				http.Error(w, "invalid end parameter", http.StatusBadRequest)
				return
			}
			end = parsed
		}

		token, err := tc.getToken()
		if err != nil {
			log.Printf("Error getting token: %v", err)
			http.Error(w, "failed to authenticate with OpenSky", http.StatusInternalServerError)
			return
		}

		apiURL := fmt.Sprintf("%s/%s?airport=%s&begin=%d&end=%d",
			openskyAPI, flightType, airport, begin, end)

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
		if err != nil {
			http.Error(w, "failed to create request", http.StatusInternalServerError)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)

		log.Printf("Fetching %s: %s", flightType, apiURL)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Error fetching %s: %v", flightType, err)
			http.Error(w, "failed to fetch flight data", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "failed to read response", http.StatusBadGateway)
			return
		}

		// OpenSky returns 404 when no flights are found for the given time range
		if resp.StatusCode == http.StatusNotFound {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		if resp.StatusCode != http.StatusOK {
			log.Printf("OpenSky API error (%d): %s", resp.StatusCode, body)
			http.Error(w, fmt.Sprintf("OpenSky API error: %d", resp.StatusCode), resp.StatusCode)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

func main() {
	creds, err := loadCredentials("opensky-credentials.json")
	if err != nil {
		log.Fatalf("Failed to load credentials: %v", err)
	}
	log.Printf("Loaded credentials for client: %s", creds.ClientID)

	tc := &tokenCache{creds: creds}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})
	http.HandleFunc("/departures", makeFlightHandler(tc, "departure"))
	http.HandleFunc("/arrivals", makeFlightHandler(tc, "arrival"))

	log.Println("Server listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
