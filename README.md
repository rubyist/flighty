# Flighty

Real-time flight departure/arrival board for DeKalb-Peachtree Airport (KPDK). A Go backend proxies the FlightAware AeroAPI and serves a single-page frontend with an airport-style display.

## Setup

1. Get a [FlightAware AeroAPI](https://www.flightaware.com/aeroapi/) key.

2. Copy the example config and fill in your key:

   ```bash
   cp config-example.json config.json
   ```

   Edit `config.json`:

   ```json
   {
     "aeroApiKey": "your-aeroapi-key",
     "homeAirport": "DTW"
   }
   ```

   - `aeroApiKey` — your FlightAware AeroAPI key
   - `homeAirport` — ICAO code of the airport to display

3. Build and run:

   ```bash
   go build -o flighty main.go
   ./flighty
   ```

4. Open [http://localhost:8080](http://localhost:8080).

## Architecture

No external Go dependencies — uses only the standard library.

- **Backend:** `main.go` — HTTP server on port 8080, proxies AeroAPI for flights and airport info, caches airport lookups to `airport-cache.json`
- **Frontend:** `index.html` — vanilla JS/HTML/CSS, auto-refreshes every hour
- **Flight detail:** `flight.html` — per-flight page with route map from AeroAPI
