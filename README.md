# DHIS2 Sync Mediator

An OpenHIM mediator that synchronizes aggregate data (dataValueSets) between two DHIS2 instances via FHIR, using HAPI FHIR as the central exchange layer.

## Architecture

```
Source DHIS2 ──> HAPI FHIR (Location + MeasureReport) ──> Target DHIS2
                          ▲
                     OpenHIM (orchestration & logging)
```

The mediator exposes 3 async endpoints, each tracked as an OpenHIM transaction:

| Endpoint | Description |
|---|---|
| `GET /pull-orgunit?ouLevel=6` | Fetch org units from source DHIS2, save as FHIR Locations |
| `GET /dhis2-to-fhir?dataSet=X&weeks=4` | Pull dataValueSets from source, save as FHIR MeasureReports |
| `GET /fhir-to-dhis2?dataSet=X&weeks=4` | Read MeasureReports from HAPI, push to target DHIS2 |

## Features

- Async processing: responds 202 immediately, updates OpenHIM transaction when done
- Concurrent worker pool for batch processing
- DHIS2 metadata preserved via FHIR extensions (period, completeDate, attributeOptionCombo)
- Deterministic FHIR resource IDs for idempotent re-runs
- Configurable org unit identifier system to avoid collisions in shared HAPI FHIR

## Configuration

Copy `.env.sample` to `.env` and fill in values:

```bash
cp .env.sample .env
```

Key variables:

| Variable | Description |
|---|---|
| `DHIS2_SOURCE_URL` | Source DHIS2 base URL |
| `DHIS2_SOURCE_PAT` | Source DHIS2 Personal Access Token |
| `DHIS2_TARGET_URL` | Target DHIS2 base URL |
| `DHIS2_TARGET_PAT` | Target DHIS2 Personal Access Token |
| `HAPI_FHIR_URL` | HAPI FHIR server URL (e.g. `https://fhir.example.com/fhir`) |
| `OU_IDENTIFIER_SYSTEM` | FHIR Location identifier system (default: `urn:dhis2:anss:organisationUnits`) |
| `DEFAULT_OU_LEVEL` | Org unit level to sync (default: `6`) |
| `DEFAULT_WEEKS` | Number of weeks to sync (default: `4`) |
| `MAX_WORKERS` | Concurrent workers (default: `5`) |

See `.env.sample` for the full list.

## Running

### Local

```bash
go run .
```

### Docker

```bash
docker compose up -d
```

### Usage

```bash
# Step 1: Pull org units to HAPI FHIR
curl "http://localhost:8001/pull-orgunit?ouLevel=6"

# Step 2: Pull data from source DHIS2 to HAPI FHIR
curl "http://localhost:8001/dhis2-to-fhir?dataSet=DATASET_ID&weeks=4"

# Step 3: Push data from HAPI FHIR to target DHIS2
curl "http://localhost:8001/fhir-to-dhis2?dataSet=DATASET_ID&weeks=4"
```

Via OpenHIM:

```bash
curl -u 'client:password' "https://openhim-router.example.com/pull-orgunit?ouLevel=6"
```

## Deployment

- Docker image published to `ghcr.io/didate/dhis2-sync-mediator:latest`
- GitHub Actions CI/CD: builds on push to `main`, deploys via SSH
- Traefik reverse proxy for HTTPS

## License

MIT
