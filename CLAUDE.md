# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Flighty is a real-time flight departure/arrival board for DeKalb-Peachtree Airport (KPDK). Go backend proxies the FlightAware AeroAPI and serves a single-page frontend with an airport-style display.

## Build and Run

```bash
# Build
go build -o flighty main.go

# Run (listens on localhost:8080)
./flighty
```

Requires `config.json` (gitignored) — copy `config-example.json` and fill in real values:
- `aeroApiKey` — FlightAware AeroAPI key
- `airportDbToken` — AirportDB API token

No external Go dependencies — uses only the standard library.

## Architecture

The entire backend is `main.go` (~300 lines) and the frontend is `index.html` (~270 lines).

**Backend (main.go):**
- HTTP server on port 8080 with four routes: `/` (serves index.html), `/flights`, `/airport`, `/config`
- Single `/flights` endpoint calls AeroAPI `/airports/{id}/flights` and returns combined departures + arrivals
- Airport info lookup with three-tier fallback: in-memory cache → AirportDB API → SkyVector API
- Airport cache uses `sync.RWMutex` and persists to `airport-cache.json`
- Factory functions (`makeFlightsHandler`, `makeAirportHandler`) create closures that capture shared state

**Frontend (index.html):**
- Vanilla JS/HTML/CSS, no build step or framework
- Dark theme airport-style board with auto-refresh every hour
- Single fetch to `/flights` returns both departures and arrivals with airport names inline
- Airport codes link to SkyVector pages

## External APIs

| Service | Purpose | Auth |
|---------|---------|------|
| FlightAware AeroAPI | Flight departures/arrivals | API key (`x-apikey` header) |
| AirportDB | Airport names/metadata | API token |
| SkyVector | Airport page links (fallback) | None |
