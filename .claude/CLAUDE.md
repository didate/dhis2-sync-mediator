# DHIS2 Sync Mediator

## Overview
An OpenHIM mediator written in Go that synchronizes aggregate data (dataValueSets) between two DHIS2 instances via FHIR. Part of Guinea's health data interoperability platform.

## Context
- **Source DHIS2**: ANSS surveillance system (`surveillance.sante.gov.gn/anss`) — the system data is pulled from.
- **Target DHIS2**: National data warehouse (`entrepot.sante.gov.gn/dhis`) — the system data is pushed to.
- **OpenHIM**: Interoperability layer hosted at `openhim-api.dev.simpetin.com` (API) / `openhim-router.dev.simpetin.com` (router).
- **Data type**: Aggregate data — weekly/periodic dataValueSets identified by dataSet, orgUnit, and period.

## Architecture
- **OpenHIM integration**: Registers as a mediator with URN `urn:mediator:dhis2-sync`, sends heartbeats every 10s, returns OpenHIM-formatted JSON responses (`application/json+openhim`) with orchestration metadata for transaction logging.
- **Sync flow** (planned):
  1. Pull dataValueSets from the **source** DHIS2 via `/api/dataValueSets` (done)
  2. Convert DHIS2 dataValues → **FHIR MeasureReport** (to do)
  3. Push FHIR MeasureReport to the **target** DHIS2 (to do)
- Each step is tracked as an **Orchestration** in the OpenHIM response, allowing visibility into each leg of the sync in the OpenHIM console.

## Project Structure
- `main.go` — HTTP server, `/sync` handler, OpenHIM response formatting
- `dhis2.go` — DHIS2 API client (`DHIS2Client`): fetches dataValueSets. Structs: `DataValueSet`, `DataValue`
- `openhim.go` — OpenHIM client (registration, heartbeat), response structs (`OpenHIMResponse`, `Orchestration`, `OHRequest`, `OHResponse`), channel config
- `config.go` — Environment-based configuration via `godotenv`, loaded from `.env`
- `.env` — Secrets and config (gitignored, never commit)

## Data Structures
- `DataValueSet`: contains metadata (dataSet, period, orgUnit, completeDate) and a slice of `DataValue`.
- `DataValue`: a single data point with dataElement, categoryOptionCombo, attributeOptionCombo, value, and audit fields.
- The FHIR conversion target is **MeasureReport** — each DataValue maps to a MeasureReport group/population entry.

## API Endpoints
- `GET /sync?dataSet=X&orgUnit=Y&period=Z` — triggers the sync flow. All three query params are required.
- `GET /health` — returns `ok`, used for liveness checks.

## Key Details
- Go module: `github.com/didate/dhis2-sync-mediator` (Go 1.23)
- Authentication to DHIS2 uses Personal Access Tokens (PAT) via `Authorization: ApiToken <pat>` header.
- Authentication to OpenHIM uses HTTP Basic Auth.
- DHIS2 client has a 60-second timeout.
- The OpenHIM channel route goes through **ngrok** for local development. The ngrok hostname is hardcoded in `openhim.go` and must be updated when it changes.
- The OpenHIM channel pattern is `^/sync.*$` with allowed role `dhis2-sync`.

## Environment Variables
| Variable | Description |
|---|---|
| `OPENHIM_API_URL` | OpenHIM Core API URL |
| `OPENHIM_API_USER` | OpenHIM API username |
| `OPENHIM_API_PASSWORD` | OpenHIM API password |
| `OPENHIM_TRUST_SELF_SIGNED` | Set `true` to skip TLS verification for OpenHIM |
| `MEDIATOR_PORT` | Port the mediator listens on (default: `8001`) |
| `MEDIATOR_URN` | Mediator URN for OpenHIM (default: `urn:mediator:dhis2-sync`) |
| `DHIS2_SOURCE_URL` | Base URL of source DHIS2 instance |
| `DHIS2_SOURCE_PAT` | PAT for source DHIS2 |
| `DHIS2_TARGET_URL` | Base URL of target DHIS2 instance |
| `DHIS2_TARGET_PAT` | PAT for target DHIS2 |

## Running
1. Start ngrok: `ngrok http 8001`
2. Update ngrok hostname in `openhim.go` if it changed
3. Run: `go run .`
4. Test directly: `curl "http://localhost:8001/sync?dataSet=ID&orgUnit=ID&period=2025W40"`
5. Test via OpenHIM: `curl -u 'dhis2-sync-client:PASSWORD' "https://openhim-router.dev.simpetin.com/sync?dataSet=ID&orgUnit=ID&period=2025W40"`
