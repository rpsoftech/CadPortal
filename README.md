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
  cmd/adduser/      creates users (the first admin)
  migrations/       golang-migrate SQL (the schema source of truth)
  internal/
    calc/           pure weight, BOM and costing math
    service/        business logic and the recalc worker
    handlers/       Gin REST API (v1)
    db/             sqlc output (edit queries/, not the generated files)
    storage/        R2 client and in-memory test store
    export/         job-bag PDF and costing CSV
    auth/           bearer-token role middleware
    password/       Argon2id hashing and the password policy
frontend/
  src/app/core/     API client, models, session auth, guards, formatting
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

Create the first admin (the password is read from the terminal without echo; at least 12
characters). Everything except login needs a signed-in user:

```bash
cd backend
go run ./cmd/adduser -email you@example.com -name "Your Name" -role admin
```

Further users are created by an admin on the **Users** screen. For scripts, pass
`-password-stdin` and pipe the password in; never put it on the command line.

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
yarn install
yarn start         # http://localhost:4200, proxies /api to localhost:8080
```

Sign in with the account you created above. The session lives in `sessionStorage`, so it
ends with the browser tab. The UI only uses the role to hide controls; the server checks
the token and role on every request.

## Configuration

See [.env.example](.env.example). Never commit real credentials: `.env` is gitignored.

## Deployment

`docker-compose.prod.yml` runs the whole stack: PostgreSQL, the API, and a web container
(nginx serving the Angular app and proxying `/api`). Only the web container is published, by
default on `127.0.0.1:8080`; the API and database are reachable only inside the compose network.

```bash
cp .env.example .env
# Fill in: JWT_SECRET (openssl rand -hex 32), POSTGRES_PASSWORD (openssl rand -hex 24),
# and the Cloudflare R2 values (S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY).

docker compose -f docker-compose.prod.yml up -d --build

# Create the first admin (prompts for the password without echo):
docker compose -f docker-compose.prod.yml exec api /app/adduser -email you@example.com -name "Your Name" -role admin
```

On Windows Git Bash, prefix the `exec` line with `MSYS_NO_PATHCONV=1` so `/app/adduser` is not
rewritten into a Windows path.

What to know before going live:

- **TLS:** the web container speaks plain HTTP. Put a reverse proxy in front that terminates TLS
  and sends `Strict-Transport-Security`. The proxy should also rate-limit `/api/v1/auth/login`
  (account lockout is per account, not per IP).
- **Storage:** use R2 in production. `STORAGE=local` keeps files in a Docker volume with no
  replication or backups and is for development only.
- **Migrations** run automatically when the API starts (`AUTO_MIGRATE=true`). Run one API
  instance; the recalculation worker is safe against overlap, but there is no need to scale it.
- **Health:** the API answers `/healthz` (process up) and `/readyz` (database reachable) on its
  own port, and both images define Docker health checks. `docker compose ps` shows them.
- **Shutdown:** `docker compose stop` sends SIGTERM; the API lets in-flight requests, uploads
  included, finish for up to 30 seconds.
- **Hardening:** both app containers run as non-root with a read-only filesystem, all Linux
  capabilities dropped and `no-new-privileges`. The API image is distroless (no shell).
- **Backups** are yours to schedule; the script does the dump and the housekeeping:

```bash
scripts/backup.sh                                  # writes backups/cad_portal-<timestamp>.dump
KEEP=30 BACKUP_DIR=/srv/cad-backups scripts/backup.sh
```

  Each dump is checked for readability before it is kept, and only the newest `KEEP` (default
  14) are retained (`KEEP=0` keeps all). Run it from cron or a systemd timer, and copy the
  dumps off the machine. They contain every password hash and the full audit log, so treat
  them like `.env`. Dumps cover the database only: with R2, file versions and backups are
  managed on the Cloudflare side.

- **Restoring** replaces the database contents:

```bash
scripts/restore.sh backups/cad_portal-20261021-020000.dump
```

  It asks you to type the database name, stops the API while tables are rebuilt, restores in a
  single transaction (a failed restore leaves the database as it was) and starts the API
  again. Use `--yes` to skip the prompt. CI runs `scripts/backup_restore_test.sh` against the
  production stack, so a backup that cannot be restored fails the build.

- **Updating:** `git pull`, then `docker compose -f docker-compose.prod.yml up -d --build`.

CI builds both images and validates the compose files when their inputs change, but does not
publish anything on a pull request or a merge.

### Releases and published images

Images are published to GitHub Container Registry only when you push a version tag:

```bash
git checkout main && git pull
git tag v1.2.3
git push origin v1.2.3
```

The **Release** workflow refuses a tag whose commit is not on `main`, then builds and pushes
`ghcr.io/rpsoftech/cadportal-api` and `ghcr.io/rpsoftech/cadportal-web` tagged `1.2.3` and `1.2`
(plus `latest` for a stable version; a pre-release such as `v1.2.3-rc1` gets only its own tag).
Each image carries build provenance and an SBOM. The first publish creates the packages as
private: open each package's settings on GitHub and set the visibility you want, or log in on
the server with a token that can read packages.

To run published images on the server instead of building there:

```bash
CADPORTAL_VERSION=1.2.3 docker compose -f docker-compose.prod.yml -f docker-compose.images.yml up -d
```

Pin an exact version rather than `latest`, so an update is a deliberate change (edit the
version, run the command again). Back up first with `scripts/backup.sh`; migrations run when
the new API starts.

## API (v1)

| Group | Endpoints |
|---|---|
| Masters | `GET/POST /masters/categories`, `PUT /masters/categories/:id`, `GET/POST /masters/tags`, `PUT /masters/tags/:id`, `GET/POST /masters/metals`, `PUT /masters/metals/:id`, `GET/POST /masters/stone-codes`, `PUT /masters/stone-codes/:id`, `GET/PUT/DELETE /masters/stone-matrix`, `GET /masters/matrix-gaps`, `GET /masters/recalc-status` |
| Designs | `POST/GET /designs` (`q` search, `limit`/`offset`, total in `X-Total-Count`), `GET /designs/:code`, `PUT/DELETE /designs/:id`, `POST /designs/import` (bulk create from CSV rows; `dry_run` validates only) |
| Variations | `POST /designs/:id/variations`, `PUT /variations/:id/base-input`, `DELETE /variations/:id` |
| Seats and BOM | `GET/PUT /variations/:id/seats`, `POST /variations/:id/clone-seats`, `GET /variations/:id/simulate-bom?code=2HD` |
| CAD vault | `POST /variations/:id/upload-cad`, `GET /variations/:id/download?kind=3dm\|stl`, `DELETE /variations/:id/cad?kind=3dm\|stl` |
| Exports | `POST /exports/job-bag-pdf`, `POST /exports/costing-csv`, `GET /exports/designs-csv` (any role) |

All paths are under `/api/v1`. Alongside the routes above:

| Group | Endpoints |
|---|---|
| Accounts | `POST /auth/login` (public), `GET /auth/me`, `POST /auth/change-password` |
| Users (admin) | `GET/POST /users`, `PUT /users/:id`, `POST /users/:id/password` |

### Master data screen

Admins maintain the reference data under **Master data** (no API calls needed):

- **Alloys:** specific gravity, wastage % (needed for costing), and the default input alloy.
- **Stone matrix:** carats per stone and rate per carat for each (size, stone code), the baseline
  stone code, and stone codes themselves. A **Missing for the baseline code** panel lists sizes
  that seats use but the matrix lacks, with an Add shortcut.
- **Categories and tags:** names, category code prefixes, and deactivating tags.

To load a large stone matrix, use **Import CSV / paste**. Columns, in order:
`size_mm, stone_code, sieve, carats_per_stone, rate_per_ct` (sieve and rate may be empty; a header
row is optional; tab-separated text pasted from a spreadsheet works). Sizes allow 2 decimals,
carats 4, rates 2; over-precise values are rejected rather than rounded. An import is
all-or-nothing, and **Export CSV** gives the same format back for editing. Stone codes must exist
before rows can use them.

Changing SG, the baseline stone code or baseline carats rebuilds every variation's weights in
the background; the screen shows when that finishes. A matrix row cannot be deleted while a seat
is locked to it.

### Importing designs

To load an existing catalog, use **Import** on the Catalog screen (admin and designer). Paste rows
from a spreadsheet or choose a CSV file. Columns, in order:
`code, name, category, tags, volume_mm3, ref_weight_g, ref_metal` (trailing empty columns may be
left out; a header row is optional).

- **Tags** go in one cell separated by `;`. Write `Group:Name` when a tag name exists in two
  groups.
- **Variations:** give a row either a volume, or a reference weight with its alloy. Repeat a
  code on another row to add a variation to the same design (`LR001-V1`, `LR001-V2`, in file
  order). A row with no base input creates the design alone.
- **Check first:** **Check** validates everything on the server and lists every problem with its
  line number, so a file can be fixed in one pass. **Import** is enabled only after a clean check
  and is all-or-nothing.
- **Never changes existing designs:** a code that is taken, including a deleted design's, is
  reported as a problem. Categories, tags and alloys must already exist.

Up to 1000 rows per import. Imports appear in the audit log as `design.import`.

**Export CSV** on the same screen (any role) downloads every design in exactly this format:
tags as `Group:Name`, one row per variation, name, category and tags on the first row of each
design. It contains no wastage or rates. The file imports cleanly into an empty installation and
rebuilds the same weights; variation numbers are assigned again in file order, so a gap left by a
deleted variation closes. Deleted designs are not exported.

### Audit log

Every state-changing request is recorded in an append-only `audit_log` table: who (a snapshot of
their email), what (`metal.update`, `user.create`, `cad.upload`, …), which entity, the HTTP
status, and a short detail such as `18KR: sg=15.25 wastage=12.5`. Also recorded: sign-ins and
failed sign-ins (with the attempted email), refused attempts (403, attributed to the user who
tried), CAD download links and exports. Admins read it on the **Audit** screen or at
`GET /api/v1/audit`.

What is deliberately *not* recorded: request bodies, passwords, tokens, plain reads, and
requests with no valid token. A failure to write an entry never fails the request. Rows cannot
be updated (a trigger enforces it). Set `AUDIT_RETENTION_DAYS` to delete entries older than that
many days (default 0 keeps everything).

### Access control

Every route except login needs `Authorization: Bearer <token>`.

| Role | Can do |
|---|---|
| `admin` | Everything, including master data and users |
| `designer` | Create and edit designs, variations, seats and CAD files; costing export |
| `production` | Read designs, simulate the BOM, job-bag PDF, CAD downloads. Never sees wastage % or stone rates |

Security behaviour worth knowing:

- Passwords are hashed with Argon2id (12 to 128 characters). Login tokens last 8 hours.
- Five failed logins lock an account for 15 minutes. Failures are indistinguishable: an
  unknown email, a wrong password and a locked or disabled account all return the same 401.
- Tokens are checked against the database on every request, so disabling a user, changing
  their role, or changing their password takes effect immediately.
- The last active admin cannot be demoted or disabled, and admins cannot disable themselves.
- Login attempts are limited per account, not per IP address. Put a rate-limiting proxy in
  front of a public deployment.

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
yarn ng build
yarn ng test --watch=false
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
