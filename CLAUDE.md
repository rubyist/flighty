# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Flighty is a real-time flight departure/arrival board for DeKalb-Peachtree Airport (KPDK). Go backend proxies the OpenSky Network API with OAuth2 authentication and serves a single-page frontend with an airport-style display.

## Build and Run

```bash
# Build
go build -o flighty main.go

# Run (listens on localhost:8080)
./flighty
```

Requires `config.json` (gitignored) — copy `config-example.json` and fill in real values:
- `openskyClientId` / `openskyClientSecret` — OpenSky Network OAuth2 credentials
- `airportDbToken` — AirportDB API token

No external Go dependencies — uses only the standard library.

## Architecture

The entire backend is `main.go` (~360 lines) and the frontend is `index.html` (~290 lines).

**Backend (main.go):**
- HTTP server on port 8080 with four routes: `/` (serves index.html), `/departures`, `/arrivals`, `/airport`
- OAuth2 client credentials flow with OpenSky Network, token cached in-memory with `sync.Mutex` (refreshed 60s before expiry)
- Airport info lookup with three-tier fallback: in-memory cache → AirportDB API → SkyVector API
- Airport cache uses `sync.RWMutex` and persists to `airport-cache.json`
- Factory functions (`makeFlightHandler`, `makeAirportHandler`) create closures that capture shared state

**Frontend (index.html):**
- Vanilla JS/HTML/CSS, no build step or framework
- Dark theme airport-style board with auto-refresh every hour
- Fetches departures/arrivals in parallel, then batch-fetches airport names for tooltips
- Airport codes link to SkyVector pages

## External APIs

| Service | Purpose | Auth |
|---------|---------|------|
| OpenSky Network | Flight departures/arrivals | OAuth2 client credentials |
| AirportDB | Airport names/metadata | API token |
| SkyVector | Airport page links (fallback) | None |
