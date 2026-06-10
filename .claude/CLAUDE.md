# DHIS2 Sync Mediator

## Overview
An OpenHIM mediator written in Go that synchronizes aggregate data (dataValueSets) between two DHIS2 instances via FHIR. Part of Guinea's health data interoperability platform.

## Context
- **Source DHIS2**: ANSS surveillance system (`surveillance.sante.gov.gn/anss`) — the system data is pulled from.
- **Target DHIS2**: National data warehouse (`entrepot.sante.gov.gn/dhis`) — the system data is pushed to.
- **OpenHIM**: Interoperability layer hosted at `openhim-api.dev.simpetin.com` (API) / `openhim-router.dev.simpetin.com` (router).
- **Data type**: Aggregate data — weekly/periodic dataValueSets identified by dataSet, orgUnit, and period.

## Architecture
- **OpenHIM integration**: Registers as a mediator with URN `urn:mediator:dhis2-sync`, sends heartbeats every 10s. Registers 3 channels. Uses async pattern: responds 202 immediately, updates transaction via OpenHIM API when done.
- **HAPI FHIR**: Central exchange layer at `fhir.dev.DOMAIN/fhir`. Org units stored as Location, data as MeasureReport.
- **Sync flow** (3 independent endpoints):
  1. `GET /pull-orgunit` — Fetch org units from source DHIS2 → save as FHIR Location in HAPI
  2. `GET /dhis2-to-fhir` — Read Locations from HAPI → pull dataValueSets from source → save as MeasureReport in HAPI
  3. `GET /fhir-to-dhis2` — Read MeasureReports from HAPI → convert to DataValueSet → push to target DHIS2
- Each endpoint processes asynchronously with a worker pool and updates the OpenHIM transaction when complete.
- DHIS2 period, completeDate, and attributeOptionCombo are preserved as FHIR extensions on MeasureReport.
- MeasureReport IDs are deterministic (`dataSet-orgUnit-period`) for idempotent re-runs.

## Project Structure
- `main.go` — HTTP server, endpoint registration, OpenHIM response structs, helper functions
- `handler_pull_orgunit.go` — `/pull-orgunit` handler: DHIS2 OrgUnits → FHIR Locations
- `handler_dhis2_to_fhir.go` — `/dhis2-to-fhir` handler: DHIS2 DataValueSets → FHIR MeasureReports
- `handler_fhir_to_dhis2.go` — `/fhir-to-dhis2` handler: FHIR MeasureReports → target DHIS2
- `dhis2.go` — DHIS2 API client: fetch org units, fetch/post dataValueSets
- `hapi.go` — HAPI FHIR REST client: put/get Location and MeasureReport, Bundle pagination
- `fhir.go` — FHIR MeasureReport structs and conversion (with DHIS2 extensions)
- `location.go` — FHIR Location struct and OrgUnit conversion
- `openhim.go` — OpenHIM client: registration (3 channels), heartbeat, transaction update
- `period.go` — ISO week period generation
- `config.go` — Environment-based configuration via `godotenv`
- `.env` — Secrets and config (gitignored, never commit)

## API Endpoints
- `GET /pull-orgunit?ouLevel=6` — Pull org units and save to HAPI FHIR as Locations
- `GET /dhis2-to-fhir?dataSet=X&weeks=4` — Pull data from source DHIS2, save as MeasureReports in HAPI
- `GET /fhir-to-dhis2?dataSet=X` — Read MeasureReports from HAPI, push to target DHIS2
- `GET /health` — Liveness check

## Key Details
- Go module: `github.com/didate/dhis2-sync-mediator` (Go 1.23)
- Authentication to DHIS2 uses Personal Access Tokens (PAT) via `Authorization: ApiToken <pat>` header.
- Authentication to OpenHIM uses HTTP Basic Auth.
- HAPI FHIR is unauthenticated (internal network).
- DHIS2 client has a 60-second timeout.
- Async pattern: each handler responds 202 to OpenHIM, processes in goroutine, updates transaction via `PUT /transactions/:id`.
- The `X-OpenHIM-TransactionID` header from OpenHIM is used to update transactions.
- Configurable worker pool via `MAX_WORKERS` (default 5).
- OpenHIM channel allowed role: `dhis2-sync`.

## Environment Variables
| Variable | Description |
|---|---|
| `OPENHIM_API_URL` | OpenHIM Core API URL |
| `OPENHIM_API_USER` | OpenHIM API username |
| `OPENHIM_API_PASSWORD` | OpenHIM API password |
| `OPENHIM_TRUST_SELF_SIGNED` | Set `true` to skip TLS verification for OpenHIM |
| `MEDIATOR_PORT` | Port the mediator listens on (default: `8001`) |
| `MEDIATOR_URN` | Mediator URN for OpenHIM (default: `urn:mediator:dhis2-sync`) |
| `MEDIATOR_HOST` | Hostname for OpenHIM route registration |
| `MEDIATOR_SCHEME` | `http` or `https` for OpenHIM route |
| `DHIS2_SOURCE_URL` | Base URL of source DHIS2 instance |
| `DHIS2_SOURCE_PAT` | PAT for source DHIS2 |
| `DHIS2_TARGET_URL` | Base URL of target DHIS2 instance |
| `DHIS2_TARGET_PAT` | PAT for target DHIS2 |
| `HAPI_FHIR_URL` | HAPI FHIR server base URL (e.g., `https://fhir.dev.DOMAIN/fhir`) |
| `DEFAULT_OU_LEVEL` | Default org unit level (default: `6`) |
| `DEFAULT_WEEKS` | Default number of weeks to sync (default: `4`) |
| `MAX_WORKERS` | Concurrent worker count (default: `5`) |

## Running
1. Run: `go run .`
2. Test: `curl "http://localhost:8001/pull-orgunit?ouLevel=6"`
3. Then: `curl "http://localhost:8001/dhis2-to-fhir?dataSet=AhWR8jm7KQW&weeks=4"`
4. Then: `curl "http://localhost:8001/fhir-to-dhis2?dataSet=AhWR8jm7KQW"`

## Deployment
- Docker image: `ghcr.io/didate/dhis2-sync-mediator:latest`
- CI/CD: GitHub Actions builds and pushes to ghcr.io, deploys via SSH to `/opt/interop`
- Traefik reverse proxy at `sync.dev.DOMAIN`
