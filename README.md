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

## Try it locally in one go

The quickest way to see the whole thing running needs only Docker. This is the same stack as
production, with files kept on local disk:

```bash
cp .env.example .env
```

Edit `.env` and set `POSTGRES_PASSWORD` (`openssl rand -hex 24`), `JWT_SECRET`
(`openssl rand -hex 32`) and `STORAGE=local`. Leave every `S3_*` line empty. Then:

```bash
docker compose -f docker-compose.prod.yml up -d --build --wait
```

```bash
docker compose -f docker-compose.prod.yml exec api /app/adduser -email you@example.com -name "Your Name" -role admin
```

Open http://localhost:8080 and sign in. The setup banner lists what to enter first (alloys, category
prefixes, stone matrix). Stop it with `docker compose -f docker-compose.prod.yml down` (add `-v`
to also delete the data). In Git Bash on Windows, put `MSYS_NO_PATHCONV=1` in front of the `exec`
line.

If the API keeps restarting, `docker compose -f docker-compose.prod.yml logs api` says why. The
usual cause is a half-filled `S3_*` block: fill all four in or leave all four empty.

## Getting started (for development)

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
  and sends `Strict-Transport-Security`. The proxy should pass the client address in
  `X-Forwarded-For`: the web container believes that header only from private and loopback
  peers, and uses it for its own sign-in rate limit (10 a minute per client address, burst 20,
  answered with 429). Without a proxy in front, clients are limited by their own address.
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
  them like `.env`. This script is the manual option and covers the database only; the
  automatic nightly backup of the database **and the files** is the backup service, see
  Backups below.

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

### Going live: checklist

1. **A server** with Docker and Compose, a DNS name pointing at it, and ports 80 and 443 open.
   Only the web container is published, and only on `127.0.0.1:8080`.
2. **`.env`** (from `.env.example`, never committed): `POSTGRES_PASSWORD` and `JWT_SECRET`
   (`openssl rand -hex 24` and `openssl rand -hex 32`), and the R2 values (`S3_ENDPOINT`,
   `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`). Leave `STORAGE` empty.
3. **R2:** a private bucket, and an API token limited to that bucket with object read and write.
   Downloads send the browser straight to a short-lived signed link (a page navigation, so no
   CORS rule is needed).
4. **Start it:** `docker compose -f docker-compose.prod.yml up -d --build --wait` (or the
   published images, see Releases below). The API applies migrations when it starts.
5. **TLS:** run a reverse proxy in front. `deploy/Caddyfile.example` is a complete one for Caddy
   (automatic certificates, `Strict-Transport-Security`, large uploads, streaming). Through it,
   the sign-in rate limit counts each client separately, and a spoofed `X-Forwarded-For` header
   cannot be used to get around it (both checked).
6. **First admin:** `docker compose -f docker-compose.prod.yml exec api /app/adduser -email you@example.com -name "Your Name" -role admin`
   (prefix with `MSYS_NO_PATHCONV=1` in Git Bash). Then sign in and follow the setup banner:
   alloys, category prefixes and the stone matrix (see First-time setup above).
7. **Backups:** create a second R2 bucket, make the encryption key and switch the backup service
   on (see Backups below). Then try a restore once on a spare copy so you know it works for you.
   Until a good backup exists, admins see a warning in the setup checklist.
8. **After the first start**, check by hand: sign in, create a design, upload a small `.3dm`,
   download it again and compare it with the original, remove it. CI uses local disk, so it
   cannot cover your real bucket and credentials. To check a bucket before going live, run
   `go test ./internal/storage -run R2 -v` with `R2_TEST_*` set in `.env` (a scratch bucket, see
   `.env.example`).

Checked against a real R2 bucket: uploads (including multipart), browser downloads that return
the original bytes under the original (even accented) name, expiring and tampered links, and
deletion. Not yet verified against a real service: the published-image workflow (it first runs
when you push a version tag).

### Backups

The `backup` service (a separate small image, because the API image has no shell or `pg_dump`) does
two things every night at `BACKUP_AT` (default 02:00 `BACKUP_TZ`, UTC), into a **second bucket**:

| What | How it is stored | Kept |
|---|---|---|
| **Database** | `pg_dump -Fc` (already compressed), checked with `pg_restore --list` before it is kept, then **encrypted** to your public key (age) as `db/cad_portal-<time>.dump.age` | 7 daily, 4 weekly, 12 monthly, and **one per year for good** (`BACKUP_KEEP_*` change the first three) |
| **CAD files, photos, video** | every object the vault has and the backup lacks, copied byte for byte (same type and `Content-Encoding`) to `files/<same key>`. Objects have random names and are never rewritten, so this is incremental: night two copies only what is new | nothing is ever deleted from the backup, so a file removed in the portal stays recoverable there |

Both halves are independent (a failed file copy never stops the database dump), every run is recorded,
and the setup checklist warns admins if no good backup is newer than 36 hours (`BACKUP_MAX_AGE_HOURS`).
If the service was down, it takes a backup at start-up instead of waiting for the next night.

**What is compressed:** CAD files are gzipped by the portal before they reach R2 (flagged
`Content-Encoding: gzip`, so browsers still receive the original file), and the database dump is
compressed by `pg_dump`. Photos and video are stored as they are on purpose: JPEG and MP4 are already
compressed (gzip saves about 0%), and a gzipped video cannot be played or seeked in the browser. The
backup copies every object exactly as stored.

**Set it up (once)**

1. In Cloudflare create a second bucket, for example `cad-vault-backup`, and an API token limited to
   it with object read and write. Do not reuse the vault's token: a token that can write backups should
   not be able to delete the vault, and the other way round. Optionally add a lifecycle rule or bucket
   lock there so nothing in it can be deleted for N days.
2. Make the key pair and keep the secret half **offline** (password manager plus a printed copy).
   Without it no dump can be opened, and nobody, including the server, can recover it:

```bash
docker compose -f docker-compose.prod.yml --profile backup run --rm backup keygen
```

3. In `.env` set `COMPOSE_PROFILES=backup`, `BACKUP_S3_BUCKET`, `BACKUP_S3_ACCESS_KEY_ID`,
   `BACKUP_S3_SECRET_ACCESS_KEY` and `BACKUP_AGE_RECIPIENT` (the public key; `BACKUP_S3_ENDPOINT` defaults
   to `S3_ENDPOINT`). The service refuses to start without a recipient unless you set
   `BACKUP_UNENCRYPTED=true`, and refuses to use the vault bucket as its own backup.
4. `docker compose -f docker-compose.prod.yml up -d`, then `docker compose -f docker-compose.prod.yml logs backup`.
   To take one right now: `docker compose -f docker-compose.prod.yml run --rm backup once`.

**The Backups screen** (admins only, in the top bar) shows when the last good backup finished, every recent
run with what it copied and what went wrong, and a **Back up now** button. Pressing it only records a
request; the backup service notices it within about a minute (one request waits at a time, and it refuses
while a backup is running).

To also list the stored dumps and **download** one, give the portal a *second*, **read-only** token for the
backup bucket and set `BACKUP_VIEW_ACCESS_KEY_ID` and `BACKUP_VIEW_SECRET_ACCESS_KEY` in `.env` (the API
reads `BACKUP_S3_BUCKET` and the endpoint too). The portal never gets the token the backup service writes
with, and refuses to start if you give it that one. Without these two values the screen shows history and
"Back up now" only. A download is a short-lived signed link to the **encrypted** `.age` file, recorded in the
audit log; without the secret key kept offline it opens nothing, so a stolen admin login alone cannot read
your data from a dump. Only objects named like dumps can be downloaded this way, never the files.

**Restoring** (keep the secret key in a file, here `secret.key`):

```bash
docker compose -f docker-compose.prod.yml run --rm backup list                 # what dumps exist
docker compose -f docker-compose.prod.yml run --rm -T -e BACKUP_AGE_IDENTITY="$(cat secret.key)" \
  backup fetch db/cad_portal-20261022-020000.dump.age - > restore.dump         # download and decrypt
scripts/restore.sh restore.dump                                                # replaces the database
docker compose -f docker-compose.prod.yml run --rm backup restore-files        # puts back files the vault lacks
```

`restore-files` only adds: it never overwrites or deletes anything in the vault. `scripts/backup_service_test.sh`
runs this whole cycle in CI against a real database and an S3-compatible server, including a dump restored
into a fresh database and a deleted CAD file coming back with its encoding.

**Limits, honestly:** this is a nightly full dump, so a disaster can lose up to a day of changes (there is
no point-in-time recovery). Files are copied as they appear in the vault, so a file uploaded and lost the
same day before the night run is not in the backup. The backup bucket is only as safe as its token: keep
that token off the server's other services, and rotate it like any secret.

### Logs and uptime monitoring

**Logs.** The API writes one JSON object per line (`LOG_FORMAT=text` for a terminal, `LOG_LEVEL` to change
the detail), and so does the web container. Every request gets an id, sent back as the `X-Request-Id`
header and shown in the log line (`request_id`, method, route, status, milliseconds, bytes, user id and
role). A server error answers with that id too, so a person can quote it and you can find the exact request:

```bash
docker compose -f docker-compose.prod.yml logs api | grep 7f3a9c0b12d4e5f6
```

Query strings (which carry the token of a signed download link) and request bodies are never logged.
Failed requests are `warn`, server errors are `error` (with the internal error text), and health probes are
`debug` only. Docker keeps the last 5 files of 10 MB per container, so logs cannot fill the disk.

**Uptime.** The server only makes outgoing requests, so nothing has to be reachable from outside. Create a
check at a monitoring service (Healthchecks.io, Uptime Kuma's push monitor, Better Stack...) that expects a
ping every 5 minutes with a few minutes of grace, and put its URL in `HEARTBEAT_URL`. The API pings it while
it can reach its database and stays silent when it cannot, so the monitor alerts you if the server, the API
or the database goes down.

Do the same for backups with a second check that expects a ping every 26 hours, and put its URL in
`BACKUP_HEARTBEAT_URL`. The backup service pings only after a *good* backup, so a backup that stops or fails
alerts you even if the whole server is gone. These URLs are secret-like and are never written to the logs
(only the host is).

### Forgotten passwords

Two ways, and neither lets anyone see or choose someone else's password:

- **An admin makes a reset link** (Users screen, **Reset link**). It is a one-hour, single-use link; the
  admin passes it on (chat, phone, in person) and the user chooses their own password with it. It needs no
  mail server. Making a new link cancels the old one, and a disabled account cannot get one. (**Set
  password** is still there for when an admin has to type one.)
- **The user asks for it on the sign-in page** (**Forgot password?**), if you set up email. In `.env` put the
  public address of the portal in `APP_BASE_URL` (no path) and the mail server in `SMTP_HOST`, `SMTP_PORT`
  (587), `SMTP_USERNAME`, `SMTP_PASSWORD` and `SMTP_FROM`. The connection must use STARTTLS (port 587) or
  implicit TLS (`SMTP_TLS=implicit`, port 465); the service refuses to send in the clear unless you set
  `SMTP_TLS=none` for a local mail catcher. Without `SMTP_HOST` the page says to ask an admin.

How it is protected: the page answers the same whether or not the address has an account; a link works once
and for one hour; only a hash of it is stored, so a copy of the database cannot reset anyone; at most 3
emails an hour per account, and 5 requests a minute per client address (the web container's limit); a
disabled account gets nothing; a weak new password is refused without using up the link; and using a link
ends every session of that user and unlocks the account. Requests, links and resets are in the audit log
(never the token). Checked end to end against a real SMTP server (Mailpit) with the link from the delivered
email, in the browser tests, and in CI.

### Releases and published images

Two kinds of images are published to GitHub Container Registry (`cadportal-api`, `cadportal-web`
and `cadportal-backup`), and they never mix:

| Where | Tag | Built when |
|---|---|---|
| **Staging** | `staging` (and `sha-<commit>` to pin or roll back one build) | every commit that lands on `main` after CI passed on it (`staging.yml`) |
| **Production** | `1.2.3` and `1.2` | only when you push a version tag (`release.yml`) |

There is no `latest`, so nothing can reach production by accident. A release:

```bash
git checkout main && git pull
git tag v1.2.3
git push origin v1.2.3
```

The **Release** workflow refuses a tag whose commit is not on `main`. A pre-release such as
`v1.2.3-rc1` gets only its own tag. Each image carries build provenance and an SBOM. The first publish
creates the packages as private: open each package's settings on GitHub and set the visibility you
want, or log in on the server with a token that can read packages (`docker login ghcr.io`, a token with
only `read:packages`).

### Staging and production on servers

`scripts/deploy.sh` pulls published images and restarts **only the application containers**. The
database container and its volume are never touched: no `down`, no `-v`, no `rm`, and `--no-deps`
keeps compose from recreating the database. `scripts/deploy_test.sh` checks that in CI (a fake docker
records every call, so adding a `down` fails the build).

**Staging server** (follows `main`)

1. Clone the repository to `/opt/cadportal` (the scripts and compose files), make `.env` from
   `.env.example`, and give staging **its own everything**: its own `POSTGRES_PASSWORD` and
   `JWT_SECRET`, its own R2 buckets (vault and backup) with their own tokens, and
   `COMPOSE_PROJECT_NAME=cadportal-staging`. Never point staging at the production buckets: a staging
   bug could then damage real files.
2. `docker login ghcr.io` once as the deploy user (token with `read:packages`).
3. Run it once by hand: `scripts/deploy.sh staging`. Then create the first admin as in "Going live".
4. Let a timer do it from now on (every 5 minutes it pulls, and restarts only when an image changed):

```bash
sudo cp deploy/systemd/cadportal-staging-update.* /etc/systemd/system/   # edit User= and paths first
sudo systemctl daemon-reload && sudo systemctl enable --now cadportal-staging-update.timer
journalctl -u cadportal-staging-update.service -n 30                    # what it did
```

   The server pulls; GitHub never needs SSH access to it, and nothing on the server listens for
   deployments. Put the TLS proxy (`deploy/Caddyfile.example`) in front with the staging domain.

**Production server** (tagged releases only, a deliberate step)

```bash
scripts/deploy.sh 1.2.3
```

It refuses anything that is not a version number, takes a backup first (`backup once`) and stops if
that fails, pulls, then restarts api, web and backup with `--wait` until they are healthy. Without the
backup service enabled it refuses unless you pass `--skip-backup`. Production is not on a timer on
purpose: a migration runs when the new API starts and starting the old image again does not undo it.
To go back, run the script with the earlier version only if the newer release added no migration;
otherwise restore the backup it took.

To run published images by hand instead:

```bash
CADPORTAL_VERSION=1.2.3 docker compose -f docker-compose.prod.yml -f docker-compose.images.yml up -d --no-deps api web
```

## API (v1)

| Group | Endpoints |
|---|---|
| Masters | `GET/POST /masters/categories`, `PUT /masters/categories/:id`, `GET/POST /masters/tags`, `PUT /masters/tags/:id`, `GET/POST /masters/metals`, `PUT /masters/metals/:id`, `GET/POST /masters/stone-codes`, `PUT /masters/stone-codes/:id`, `GET/PUT/DELETE /masters/stone-matrix`, `GET /masters/matrix-gaps`, `GET /masters/recalc-status` |
| Designs | `POST/GET /designs` (POST assigns the code, a supplied one is refused; `q` search, `limit`/`offset`, total in `X-Total-Count`), `GET /designs/:code`, `PUT/DELETE /designs/:id`, `POST /designs/import` (bulk create from CSV rows; `dry_run` validates only) |
| Variations | `POST /designs/:id/variations`, `PUT /variations/:id/base-input`, `DELETE /variations/:id` |
| Seats and BOM | `GET/PUT /variations/:id/seats`, `POST /variations/:id/clone-seats`, `GET /variations/:id/simulate-bom?code=2HD` |
| CAD vault | `POST /variations/:id/upload-cad`, `GET /variations/:id/download?kind=3dm\|stl`, `DELETE /variations/:id/cad?kind=3dm\|stl` |
| Exports | `POST /exports/job-bag-pdf`, `POST /exports/job-bags-pdf` (many bags in one PDF), `POST /exports/costing-csv`, `GET /exports/designs-csv` (any role) |

All paths are under `/api/v1`. Alongside the routes above:

| Group | Endpoints |
|---|---|
| Accounts | `POST /auth/login` (public), `GET /auth/me`, `POST /auth/change-password` |
| Users (admin) | `GET/POST /users`, `PUT /users/:id`, `POST /users/:id/password` |

### Master data screen

Admins maintain the reference data under **Master data** (no API calls needed):

- **Alloys:** specific gravity, wastage % (needed for costing), and the default input alloy.
- **Stone matrix:** weight per stone (entered in grams, or carats) and a price per piece (the default) or per carat for each (size, stone code), the baseline
  stone code, and stone codes themselves. A **Missing for the baseline code** panel lists sizes
  that seats use but the matrix lacks, with an Add shortcut.
- **Categories and tags:** names, category code prefixes, and deactivating tags. Type a prefix
  straight into the table; it saves when you leave the field.

### First-time setup

A fresh installation has the alloys and their specific gravities but none of your business
data. Costing and design numbering do not work until an admin enters it, and the app says what
is missing: the Catalog shows a banner, and the top of **Master data** lists every open item
and shrinks as you fix them.

- **Alloys:** wastage % for every active alloy (costing refuses to run without it; it never
  assumes zero) and the default input alloy.
- **Category code prefixes:** 1 to 6 letters per category, for example `LR`. **Design codes are
  assigned automatically**, never typed: the prefix plus a 4-digit number counted per category
  (`LR0001`, `LR0002`, …; past 9999 the number simply grows). A category without a prefix cannot
  take new designs. Changing a prefix later restarts the count for new designs and never
  renames existing ones. Two categories cannot share a prefix.
- **Stone matrix:** the stone sizes with weight per stone (grams or carats) and price (per piece, or per carat) (use **Import CSV
  / paste** for a long list), and a baseline stone code. Sizes your seats use but the matrix
  lacks are listed as gaps.

Items are marked *required* when something does not work without them and *recommended*
otherwise (for example a matrix row with no rate: costing refuses to price that stone).

**Keeping a copy of the master data:** **Export all (ZIP)** at the top of Master data downloads
one zip of CSV files (admin only): `alloys.csv` (with wastage %), `stone_codes.csv`,
`stone_matrix.csv`, `categories.csv` (with prefixes and the last number used) and `tags.csv`,
plus a `README.txt`. `stone_matrix.csv` is exactly the stone matrix import format, so it can be
loaded back as it is; the others are records to keep and read. The zip contains wastage and
rates, so store it as carefully as a database backup.

**Stone weight is in grams.** A row's weight per stone is typed in grams (the unit has a switch to
carats) and the screen shows the other unit beside it: 0.0014 g is 0.0070 ct, converted exactly
(1 ct = 0.200 g). Carats are what is stored, to 8 decimals (a supplier list in grams per 1000 pieces needs 5, and the trade quotes carats to 7 or 8), so a weight that would need rounding
(0.000000001 g is 0.000000005 ct) is refused with that reason instead of being rounded. Grams lead everywhere
stone weight is shown: the matrix, the BOM, the weight matrix, the job bag, the catalog filter.

**Stones are priced per piece by default, or per carat.** Each row has a price and a basis (*per
piece* or *per carat*); costing multiplies the stone count by the price per piece, or the carats by the
price per carat, line by line (so one design can mix both). A row remembers a price for each basis but
only the one named by its basis is used, and the screens, the setup checklist and a costing error all say
which one is missing ("price per piece not set for 1.10 mm × 2HD"). Rows that already had a per-carat
price keep meaning per carat.

To load a large stone matrix, use **Import CSV / paste** with a header row, for example
`size_mm, stone_code, sieve, grams_per_stone, rate_per_pc, rate_per_ct, rate_basis`. Columns may come
in any order; the weight is `grams_per_stone` or `carats_per_stone`, and the price is `rate_per_pc`
and/or `rate_per_ct` with `rate_basis` (`pc` or `ct`; empty means per piece, or per carat when only a
per-carat price is given). Without a header the older five columns (size, code, sieve, carats, rate per
carat) and the eight columns of **Export CSV** are read by position; sieve and prices may be empty and
tab-separated text pasted from a spreadsheet works. Sizes allow 2 decimals, carats 8, prices 2;
over-precise values are rejected rather than rounded. An import is all-or-nothing, and **Export CSV**
gives the same eight columns back for editing. Stone codes must exist before rows can use them.

**A ready-made list: CZ stones.** `docs/imports/cz-stone-weights.csv` is the supplier's weight list
(grams per 1000 pieces for STAR, MHD and HD stones, sizes 1 mm to 2/00) converted to grams per
stone, 35 rows. To load it: add the stone codes `STAR`, `MHD` and `HD` under **Stone codes** (**Add
stone code**), then **Import CSV / paste** the file's contents. Notes on how it was made:
- The supplier's size names (`1/10`, `2/00`) are read as millimetres (1.10, 2.00), the usual jeweller's
  notation, and kept in the **Sieve** column, so the original name is never lost. Check this reading.
- A `0` in the list means that stone does not come in that size, so no row is made for it (STAR at
  1/15, 1/25 and 1/35). A zero is never recorded as a weight, and blank cells are skipped.
- Prices are left empty for you to fill in (per piece by default).
- Per 1000 weights with two decimals need five decimals of carats (1.81 g per 1000 is 0.00905 ct),
  which is why the matrix keeps eight decimals of carats. Grams are shown in full: a weight of n decimals of carats is n + 1 decimals of grams (0.12345678 ct is 0.024691356 g).

Changing SG, the baseline stone code or baseline carats rebuilds every variation's weights in
the background; the screen shows when that finishes. A matrix row cannot be deleted while a seat
is locked to it.

### Photos and video

Every design page has a **Photos & video** section: one group for the whole design and one for
each variation. Each group holds up to **5 files, photos and videos together**.

- **Who:** admins and designers add, delete and choose covers; every role, production included,
  can look.
- **Types:** JPEG, PNG or WebP photos up to 20 MB, and MP4, MOV (iPhone) or WebM video up to
  300 MB. The type is decided from the file's content, never its name or what the browser says,
  so a script renamed `.jpg` or a HEIC photo is refused with a clear message.
- **Cover:** a photo of the whole design can be made the **cover**; it is the picture on the
  design's card in the catalog. Without one, the first photo is used. A design with no photo shows
  "No photo yet".
- **Thumbnails** (480 px, JPEG) are made when a photo is uploaded, so the catalog loads small
  files. The original is stored untouched. Phone photos are rotated correctly from their EXIF
  orientation. The original keeps its EXIF data, including GPS if the camera recorded it.
- **Storage:** the same R2 bucket under `media/<design code>/`, or local disk in development.
  Photos and video are shown straight from the bucket through short-lived signed links (an hour),
  and a video can be seeked because the object is stored as it is, not gzipped.
- **Content-Security-Policy:** the web container allows images and media from the R2 endpoint
  only (`https://*.r2.cloudflarestorage.com`). If you reach R2 through a custom domain, set
  `MEDIA_ORIGINS` in `.env` to that origin.
- **Job bags carry the photo:** the job-bag PDF prints a photo at the top right, so the bench
  sees what to make. It is the variation's own first photo, else the design's cover, else the
  design's first photo (the 480 px thumbnail, sharp enough at 50 mm). A design without photos, or
  storage that cannot be reached, gives the same sheet as before: the photo is never a reason for
  a job bag to fail.
- **Job bags carry a QR code:** top right, 30 mm with its quiet zone and the highest error
  correction, with the variation code printed under it. With `APP_BASE_URL` set it holds
  `<APP_BASE_URL>/designs/LR0001?v=LR0001-V1`: a phone scan opens the design page with that variation
  selected. Without it, it holds the plain text `LR0001-V1`. Only codes go into it, never names,
  weights or prices.
- **Many job bags in one PDF:** on a design page, **Print all job bags** (every variation, with the
  chosen metal and stone code) or tick variations and **Print selected**. The API is
  `POST /exports/job-bags-pdf`: `items` (`variation_id`, optional `metal_code`/`stone_code`) and/or
  `design_ids`, with shared `metal_code`/`stone_code`; at most 200 bags, in the order given, "Page n of N"
  in the footer. If any bag lacks stone-weight rows the answer is 422 listing every such variation
  and nothing is printed; with `skip_incomplete: true` the complete bags are printed, the skipped ones
  are named in the `X-Skipped` header and on a last page. For a catalog selection bar,
  `ApiService.jobBagsPdf()` is ready to call. Each print is audited as `export.job_bags` (the count).
- Deleting a media file, or a variation, removes its stored objects. Uploads, deletions and cover
  changes are in the audit log (`media.upload`, `media.delete`, `media.cover`).

### Undo: the Removed screen

Admins and designers have a **Removed** link in the top bar for taking back a mistake.

- **Deleted designs** stay there until someone restores them, with their code, variations, seats and
  files. A restored design is exactly as it was.
- **Removed and replaced files** (CAD files, photos, videos) are not deleted from storage straight
  away: they are kept for `DELETED_FILE_RETENTION_DAYS` (default 30) and then deleted for good by a
  background job that runs hourly. Until then **Restore** puts the file back where it was, if the place
  is free: a CAD file needs its variation to have no file of that kind and its checksum to be unused
  (otherwise the screen says which variation has it), and a photo or video needs room (5 per design or
  variation) and a live design. A file that was **replaced** by a newer upload can be restored only
  after the newer one is removed.
- **A deleted variation** cannot be restored from the app (its seats and weights are gone), but its
  files are kept for the same period; whoever runs the server can recover them from the bucket by the
  keys in the `trashed_files` table.
- Set `DELETED_FILE_RETENTION_DAYS=0` to delete files at once, as before. Storage is only freed when the
  period ends, so a busy catalog uses a little more space for 30 days.
- Restores are in the audit log (`trash.restore_file`, `design.restore`).
- This is a safety net, not a backup: after the period the file is gone from R2 unless you turned on
  R2 object versioning or keep your own copy.

### Importing designs

To load an existing catalog, use **Import** on the Catalog screen (admin and designer). Paste rows
from a spreadsheet or choose a CSV file. Columns, in order:
`code, name, category, tags, volume_mm3, ref_weight_g, ref_metal` (trailing empty columns may be
left out; a header row is optional).

- **Codes:** leave `code` empty. Each row that has a category starts a new design and gets the
  next number of that category (`LR0001`, `LR0002`, …, in file order). A code is only for
  bringing over designs that already have one, such as an earlier export: it must be the
  category's prefix plus digits, and the numbering carries on after the highest one.
- **Tags** go in one cell separated by `;`. Write `Group:Name` when a tag name exists in two
  groups.
- **Variations:** give a row either a volume, or a reference weight with its alloy. A row with
  no category (and no code) adds another variation to the design above it, and so does repeating
  that design's code (`LR0001-V1`, `LR0001-V2`, in file order). A row with no base input creates
  the design alone.
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
- Login attempts are limited per account (5 failures lock it for 15 minutes) and, in the web
  container, per client address (10 a minute, burst 20), which also stops one client guessing
  across many accounts. The address limit trusts `X-Forwarded-For` only from private and
  loopback peers; the API itself does not see client addresses, so keep it unpublished behind
  the web container as the compose file does.

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

### Browser smoke tests

CI also drives the real production stack in a browser (Playwright, `frontend/e2e/`): sign in,
import designs, check the weight matrix against the spec fixtures, upload and remove a CAD
file, read the audit log, and confirm a production user cannot edit. To run them yourself
against a **throwaway** stack (the tests create designs `E2E001` and `E2E002` and two users):

```bash
STORAGE=local WEB_PORT=18080 POSTGRES_PASSWORD=... JWT_SECRET=... \
  docker compose -f docker-compose.prod.yml up -d --build --wait
(cd frontend && yarn install && yarn playwright install chromium)
WEB_PORT=18080 scripts/e2e.sh
docker compose -f docker-compose.prod.yml down -v     # the next run needs a fresh database
```

`scripts/e2e.sh` creates the two users with random passwords; pass Playwright options after it
(for example `--grep "weight matrix"` or `--headed`). On a CI failure the report and traces are
attached to the run as the `playwright-report` artifact.

## Regenerating sqlc code

sqlc is run through its official Docker image:

```bash
cd backend
docker run --rm -v "$PWD:/src" -w /src sqlc/sqlc:1.31.1 generate
```

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). Repository
protection settings are listed in [docs/github-settings.md](docs/github-settings.md).
