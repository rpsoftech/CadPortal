# CadPortal

Internal manufacturing catalog, CAD/CAM file vault and Job-Bag spec generator for
jewelry production.

Master designs (`LR001`) hold CAD variations (`LR001-V1`, `LR001-V2`). Each variation
has a base input (volume, or a reference weight in a reference alloy), its CAD files and
its stone seats. The portal computes net and gross weights for every active metal,
resolves the stone BOM for a chosen stone code, and exports a job-bag PDF and a costing
CSV.

> **License:** source-available for **personal use only**. Commercial or business use,
> selling, and hosting it for others all need the owner's written permission. See
> [LICENSE](LICENSE).

**Out of scope:** public storefront, customer payments, live spot-gold pricing.

## Status

| Stage | Scope | State |
|---|---|---|
| 1 | Masters, design/variation CRUD, weight calculation | Done |
| 2 | Stone seats, `simulate-bom` | Done |
| 3 | CAD upload/download (R2, presigned URLs, role-gated) | Done |
| 4 | Job-bag PDF, costing CSV | Done |
| 5 | Angular screens (catalog, variation editor, design detail) | Done |

## Stack

- **Backend:** Go, Gin, pgx/v5 with sqlc, golang-migrate, PostgreSQL
- **Money and weights:** `shopspring/decimal` (never `float64`); grams stored to 3 dp,
  carats to 4 dp
- **Files:** streamed SHA-256 (of the original bytes) and gzip into Cloudflare R2, served
  through short-lived presigned URLs
- **Frontend:** Angular 22 (standalone components, Signals, zoneless), Angular Material, Vitest

## Layout

```
backend/
  cmd/api/          API server
  cmd/devtoken/     mints dev-only bearer tokens
  migrations/       golang-migrate SQL (the schema source of truth)
  internal/
    calc/           pure weight, BOM and costing math
    service/        business logic and the recalc worker
    handlers/       Gin REST API (v1)
    db/             sqlc output (edit queries/, not the generated files)
    storage/        R2 client and in-memory test store
    export/         job-bag PDF and costing CSV
    auth/           bearer-token role middleware
frontend/
  src/app/core/     API client, models, token auth, formatting
  src/app/features/ catalog, design-detail, variation-editor screens
  src/app/shared/   dialogs, upload dropzone, base-input form
docker-compose.yml  local PostgreSQL
```

## Getting started

You need Go (see `backend/go.mod`), Docker, Node 22.22.3+ (or 24.15+) for the frontend, and a shell.

```bash
# 1. Start Postgres and create the test database
docker compose up -d
docker compose exec db psql -U cad -d cad_portal -c "create database cad_portal_test"

# 2. Configure (the server reads real environment variables, not .env directly)
cp .env.example .env
#    then export the variables from .env into your shell, and set JWT_SECRET
#    to a random string of 32+ characters

# 3. Run the API (AUTO_MIGRATE=true applies the migrations)
cd backend
go run ./cmd/api
```

Mint a development token for the role-gated routes:

```bash
JWT_SECRET=<your secret> go run ./cmd/devtoken admin
```

### File storage

| Setting | Use | Behaviour |
|---|---|---|
| `S3_BUCKET` + `S3_*` | **Production** | Cloudflare R2; downloads use presigned URLs |
| `STORAGE=local` | Development | Files under `LOCAL_STORAGE_DIR` (default `backend/data/uploads`); downloads use short-lived signed links served by the API |
| neither | | The API still runs; CAD upload/download routes return 503 |

`STORAGE=local` is for development and tests only: it logs a warning at startup, and it
has no replication or backups. Local links are signed with a key derived from
`JWT_SECRET` and expire after 10 minutes. An explicit `STORAGE` value wins over
`S3_BUCKET`.

### Frontend

```bash
cd frontend
npm ci
npm start          # http://localhost:4200, proxies /api to localhost:8080
```

There is no login service yet. Click **Sign in** and paste a token from `devtoken`; it is
kept for the browser tab only (sessionStorage). The UI uses the role only as a hint, and
the server verifies every token.

## Configuration

See [.env.example](.env.example). Never commit real credentials: `.env` is gitignored.

## API (v1)

| Group | Endpoints |
|---|---|
| Masters | `GET/POST /masters/categories`, `GET/POST /masters/tags`, `GET /masters/metals`, `PUT /masters/metals/:id`, `GET/PUT /masters/stone-matrix` |
| Designs | `POST/GET /designs`, `GET /designs/:code`, `PUT/DELETE /designs/:id` |
| Variations | `POST /designs/:id/variations`, `PUT /variations/:id/base-input` |
| Seats and BOM | `GET/PUT /variations/:id/seats`, `POST /variations/:id/clone-seats`, `GET /variations/:id/simulate-bom?code=2HD` |
| CAD vault | `POST /variations/:id/upload-cad`, `GET /variations/:id/download?kind=3dm\|stl` |
| Exports | `POST /exports/job-bag-pdf`, `POST /exports/costing-csv` |

All paths are under `/api/v1`. Roles are `admin`, `designer` and `production`. Currently
only the CAD vault and export routes require a token.

Missing data is never treated as zero: a missing stone-matrix row, wastage % or stone
rate returns `422` listing exactly what is missing.

## Testing

Tests run against the local PostgreSQL from Docker Compose.

```bash
cd backend
go vet ./...
go test ./...
```

Frontend (from `frontend/`):

```bash
npx ng build
npx ng test --watch=false
```

The tests wipe databases whose names end in `_test`, and refuse to touch any other
database.

## Regenerating sqlc code

sqlc is run through its official Docker image:

```bash
cd backend
docker run --rm -v "$PWD:/src" -w /src sqlc/sqlc:1.31.1 generate
```

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). Repository
protection settings are listed in [docs/github-settings.md](docs/github-settings.md).
