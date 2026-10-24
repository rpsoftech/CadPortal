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

**Sign-out and session length.** Two limits apply, both set on the server so they change with a restart
and no rebuild:

- `SESSION_HOURS` (default 8, 1 to 720) is the absolute life of a login. It is the expiry of the token the
  server signs; activity does not extend it and there is no refresh token. After that, or on a `401`, the
  app returns to the sign-in page.
- `IDLE_TIMEOUT_MINUTES` (default 30, 0 to 1440, 0 = off) signs the user out after that long with no
  activity: mouse, key, touch or scroll in *any* tab of the same browser (the last-activity time is shared
  through `localStorage`; if that is unavailable each tab counts its own). About a minute before, a dialog
  says "You will be signed out in N s" with a *Stay signed in* button; with a timeout of 1 minute the
  warning is 30 s. Requests the app makes on its own do not count as activity. The app reads the value from
  the public `GET /api/v1/auth/config` (`idle_timeout_minutes`) when someone signs in. The idle timeout
  is enforced by the browser, not the server: it protects an unattended screen, not a stolen token (that is
  what `SESSION_HOURS` and the per-request token check are for).

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
- **Migrations** run automatically when the API starts (`AUTO_MIGRATE=true`). Instances starting
  together queue on a database lock, so running more than one API is safe: see
  [Running more than one API instance](#running-more-than-one-api-instance).
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
two things at `BACKUP_AT` (default one run a night, 02:00 `BACKUP_TZ`, UTC; or several, see
[How often](#how-often)), into a **second bucket**:

| What | How it is stored | Kept |
|---|---|---|
| **Database** | `pg_dump -Fc` (already compressed), checked with `pg_restore --list` before it is kept, then **encrypted** to your public key (age) as `db/cad_portal-<time>.dump.age` | 7 daily, 4 weekly, 12 monthly, and **one per year for good** (`BACKUP_KEEP_*` change the first three); the newest dump of each period, plus every dump of the last 48 hours (`BACKUP_KEEP_RECENT_HOURS`) |
| **CAD files, photos, video** | every object the vault has and the backup lacks, copied byte for byte (same type and `Content-Encoding`) to `files/<same key>`. Objects have random names and are never rewritten, so this is incremental: night two copies only what is new | nothing is ever deleted from the backup, so a file removed in the portal stays recoverable there |

Both halves are independent (a failed file copy never stops the database dump), every run is recorded,
and the setup checklist warns admins if no good backup is newer than 36 hours (`BACKUP_MAX_AGE_HOURS`).
If the service was down, it takes a backup at start-up instead of waiting for the next run (when the last
good one is older than the longest gap in the schedule plus two hours: 26 hours for one run a night).

#### How often

`BACKUP_AT` is one time or a comma-separated list of 24-hour times in `BACKUP_TZ`, for example
`BACKUP_AT=02:00,08:00,14:00,20:00`. Each entry is checked (a bad one stops the service with a message),
duplicates are dropped, the list is sorted, and the next run is the soonest one after now. Dump names carry
the time, so several a day are fine. Retention keeps, as before, the *newest* dump of each of the last 7 days /
4 weeks / 12 months and of every year, and now also **every** dump taken in the last `BACKUP_KEEP_RECENT_HOURS`
(default 48; 0 turns this off), so 02:00, 08:00, 14:00 and 20:00 of yesterday and today are all still there and
older days thin out to the last dump of the day.

If you run more than once a day, also change the two things that watch for stale backups, because their defaults
assume a nightly run: `BACKUP_MAX_AGE_HOURS` (the setup-checklist warning, default 36; use a bit over twice the
longest gap, for example 14 for every 6 hours) and the expected interval of the `BACKUP_HEARTBEAT_URL` monitor.
The start-up catch-up adjusts itself.

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

**Limits, honestly:** the recovery point is the time of the last good dump: up to a day lost with the default
single nightly run, up to the longest gap in `BACKUP_AT` (6 hours for the four-times-a-day example) otherwise.
A full dump of this catalog is small, so running it every few hours costs little. True point-in-time recovery
(restore to any second) is deliberately **not built**: it needs WAL archiving, meaning a base backup plus a
continuous stream of WAL files shipped to the backup bucket (`archive_command`, or a tool such as pgBackRest or
WAL-G), a restore procedure that replays them to a chosen time, and monitoring that the archive is not falling
behind. That is more moving parts to run and to rehearse than this portal's size justifies today; if losing
a few hours of work is not acceptable, that is the next step and it is a decision for the owner. Files are copied as they appear in the vault, so a file uploaded and lost the
same day before the night run is not in the backup. The backup bucket is only as safe as its token: keep
that token off the server's other services, and rotate it like any secret.

### Running more than one API instance

One API container is plenty for a workshop, but nothing in the portal assumes it: you can run several
(for zero-downtime restarts, or more headroom) on the same Postgres.

```bash
docker compose -f docker-compose.prod.yml up -d --scale api=2      # or any number
```

`scripts/multi_instance_test.sh` proves this in CI: it starts two API containers on an empty database and
checks that requests really reach both, that a sign-in on one instance is accepted by the other, that 60
parallel design creations get 60 different codes, and that weight rebuilds finish with no failure or deadlock.

**Safe with several instances** (the database does the coordinating):

| What | How |
|---|---|
| Migrations at start | instances queue on a Postgres advisory lock; one migrates, the rest find nothing to do |
| Weight rebuild worker | each job is claimed with `FOR UPDATE SKIP LOCKED` and run under an advisory lock: one job at a time, never twice |
| Design codes | the category row is locked while a number is handed out |
| Sign-in lockout, password reset links | counters and one-time tokens live in the database |
| Sign-in tokens | stateless (signed with `JWT_SECRET`, which every instance must share) |
| Removed-file purge | rows are claimed with `SKIP LOCKED`, so no object is deleted twice |
| Audit retention | one instance prunes at a time (a try-lock); the others skip |
| Uptime heartbeat (`HEARTBEAT_URL`) | one instance pings per interval (a database claim); if it stops, another takes over within one interval |
| Backups | a separate service, run it once |

**Not shared between instances:**

- **File storage.** `STORAGE=local` keeps files in a Docker volume. Two API containers on the *same host* share
  that volume, so it works there (this is what the test does), but it can never span two hosts. With R2
  (`S3_*`) every instance sees the same bucket: that is the multi-host mode.
- **Sign-in rate limit.** It lives in the web (nginx) container, per client address. Run one web container, or accept that
  each has its own budget.
- Photo thumbnails are made by the instance that receives the upload (at most 2 at a time per instance); that is
  per-instance load only, not state.

**How requests are spread.** The web container resolves the `api` name for every request (Docker DNS, cached
for 10 seconds) and nginx rotates over all the addresses it gets, so new instances start receiving traffic
within seconds and a stopped one stops being used after the cache expires (nginx also retries the next address
on a connection error). Put a TLS proxy in front of the *web* container only; the API containers publish no ports.

**Scaling up and down.** `docker compose up -d --scale api=N` adds or removes API containers; the compose file
has no fixed `container_name`, so any number works, and health checks gate the web container's start. The
deploy script restarts the `api` service as a whole.

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
or the database goes down. With several API instances only one of them pings per interval.

Do the same for backups with a second check that expects a ping every 26 hours (a little over the longest
gap in `BACKUP_AT` if you back up more than once a day), and put its URL in
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
| Exports | `POST /exports/job-bag-pdf`, `POST /exports/job-bags-pdf` (many bags in one PDF), `GET /exports/designs-csv` (any role); `POST /exports/costing-csv`, `POST /exports/costing-preview` (admin/designer) |
| Costing sheets (admin/designer) | `POST/GET /costing-sheets`, `GET/DELETE /costing-sheets/:id`, `GET /costing-sheets/:id/csv`, `POST /costing-sheets/:id/recost` |
| Orders (read: any role; write: admin/designer) | `POST/GET /orders`, `GET/PUT /orders/:id`, `PUT /orders/:id/lines` (draft only), `POST /orders/:id/status`, `POST /orders/:id/clone` |

All paths are under `/api/v1`. Alongside the routes above:

| Group | Endpoints |
|---|---|
| Accounts | `POST /auth/login` (public), `GET /auth/me`, `POST /auth/change-password` |
| Users (admin) | `GET/POST /users`, `PUT /users/:id`, `POST /users/:id/password` |

### Master data screen

Admins maintain the reference data under **Master data** (no API calls needed):

- **Alloys:** specific gravity, wastage % (needed for costing), and the default input alloy.
- **Stone matrix:** weight per stone (entered in grams, or carats) and a price per piece (the default), per carat or per gram for each (size, stone code), the baseline
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
- **Stone matrix:** the stone sizes with weight per stone (grams or carats) and price (per piece, per carat or per gram) (use **Import CSV
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

**Stones are priced per piece by default, or per carat, or per gram.** Each row has a price and a basis
(*per piece*, *per carat* or *per gram*); costing multiplies the stone count by the price per piece, the
carats by the price per carat, or the line's grams by the price per gram, line by line (so one design
can mix all three). Line grams are the line's carats (stone count × carats per stone, not yet rounded)
× 0.200, so a per-gram line is priced from the exact weight, not the 4 dp figure on the BOM; money is
rounded once, at the end. The costing CSV ends with a `stone_pricing` column saying how each stone line
was priced and the quantity priced, e.g. `24 pcs x 100.00/pc; 0.0228 g x 5000.00/g`. A row remembers a price for each basis but
only the one named by its basis is used, and the screens, the setup checklist and a costing error all say
which one is missing ("price per piece not set for 1.10 mm × 2HD"). Rows that already had a per-carat
price keep meaning per carat.

To load a large stone matrix, use **Import CSV / paste** with a header row, for example
`size_mm, stone_code, sieve, grams_per_stone, rate_per_pc, rate_per_ct, rate_per_g, rate_basis`. Columns
may come in any order; the weight is `grams_per_stone` or `carats_per_stone`, and the price is
`rate_per_pc`, `rate_per_ct` and/or `rate_per_g` with `rate_basis` (`pc`, `ct` or `g`; empty means per
piece, or per carat when only a per-carat price is given). `rate_basis` `g` needs a `rate_per_g`. Files
made before per-gram prices (no `rate_per_g` column) still import. Without a header the older five
columns (size, code, sieve, carats, rate per carat), the eight columns of an older **Export CSV** and
the nine columns of the current one are read by position; sieve and prices may be empty and
tab-separated text pasted from a spreadsheet works. Sizes allow 2 decimals, carats 8, prices 2;
over-precise values are rejected rather than rounded. An import is all-or-nothing, and **Export CSV**
gives the same nine columns back for editing. Stone codes must exist before rows can use them.

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
- Deleting a media file removes its stored object (after the retention period, see Undo below). A deleted variation keeps its photos until it is purged. Uploads, deletions and cover
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
- **Deleted variations** are listed too, with their design, when they were deleted and how many days
  are left. **Restore** brings back the variation exactly as it was: base input, stone seats, CAD
  files and photos, with its weights worked out again. While a variation is deleted it is invisible and
  cannot be edited, downloaded or used in a job bag; its code (`LR0001-V2`) is never given to a new
  variation, and its CAD checksums stay reserved, so uploading the same file to another variation is
  refused with a message saying it is kept on a deleted variation. A variation whose design is
  deleted says "Restore the design first". After `DELETED_FILE_RETENTION_DAYS` the hourly job deletes
  the variation and its stored files for good (with `0`, deletion is immediate and the number is
  free again, as it always was). Variations deleted before this existed have their files in the
  list below, "Cannot be put back", as before.
- Set `DELETED_FILE_RETENTION_DAYS=0` to delete files at once, as before. Storage is only freed when the
  period ends, so a busy catalog uses a little more space for 30 days.
- Restores are in the audit log (`trash.restore_file`, `design.restore`, `variation.restore`).
- This is a safety net, not a backup: after the period the file is gone from R2 unless you turned on
  R2 object versioning or keep your own copy.

### Catalog filters, stone code and stone %

The catalog filters by category, text (code or name), tags, **target purity** (the metal), net and gross weight
ranges, **stone code** (1HD, 2HD, 3HD, ...) and a **stone %** range, and sorts by design code, net weight, gross
weight or stone %. The stone figures are worked out **on the fly** for the chosen metal and stone code from the
seats and the stone size matrix (the weight cache only holds the baseline code): flexible seats use the chosen
code, locked seats keep their own, with the same rounding as the job bag (carats to 4 dp, stone grams and gross to
3 dp). Stone % is stone weight / **gross** weight x 100, rounded to 2 dp (e.g. the fixture in 18KR with 2HD: 0.0694 g
over 3.119 g is 2.23 %); the min and max are inclusive and compare the rounded value. A variation with a missing
matrix row for the chosen code has no stone figure: it is excluded by any gross or stone % filter, never treated as
0, and its card says "no data for 2HD". A design matches when one of its variations does; the card shows that
variation's net, stone g, gross and stone %. Stone % and sorting need a target purity. All filters, the sort and the
page are in the address (shareable; coming back from a design restores them), and the ticked designs stay ticked.

`GET /api/v1/designs` adds `stone_code` (default: the baseline code), `min_stone_pct`, `max_stone_pct` (0 to 100, at
most 2 decimals) and `sort` (`net_asc|net_desc|gross_asc|gross_desc|stone_pct_asc|stone_pct_desc`). Any role may
use them; no price or wastage is involved. The gross range now uses the gross for the chosen stone code.

Benchmark (`TestScaleRecalc`, 5,000 designs / 15,000 variations / 60,000 seats, one Postgres 18 container on a
laptop): list with a metal and no stone filter about 0.1 s; with a stone % range, gross range or a stone % sort
about 0.6 s (every variation's seats are summed); text search, plain paging under 50 ms. No index was needed:
`variation_stone_seats (variation_id, position)` already serves the per-variation seat lookup.

### Duplicating a design

**Duplicate** (design page, admins and designers) makes a new design in the same category with the
next automatic code (`LR0007` becomes `LR0008`). The dialog asks for a name (default
"<name> (copy)"), which variations to copy (default all) and whether to copy the tags. Each copied
variation is renumbered (`-V1`, `-V2`, ...) and keeps its base input and stone seats, sizes, counts and
locks; its weights are worked out again in the same transaction, and the whole copy is all or nothing.
**CAD files, photos and videos are not copied**: a CAD file can be stored only once (checksums are
unique), so upload them to the new design yourself. A deleted design cannot be duplicated, and deleted
variations are never copied. The audit log records `design.duplicate` ("from LR0007").

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

### Costing several designs, and costing sheets

Admins and designers can cost many designs in one go. In the **Catalog**, tick the cards (or *Select
all on this page*; the selection survives paging and filtering), then **Cost selected** in the bar at
the bottom. Pick the metal and stone code, give the gold rate and the setting rate, and **Preview**:
one row per variation (design, code, variation, net, gross, stone, total) and a total. **Download
CSV** gives one file with the same columns as the single-design costing CSV plus a `TOTAL` row; the
variation code carries the design code. Up to 500 variations at a time (beyond that the API answers 400).

Missing data is never costed as zero. If a variation has no wastage % for the metal, no price for a
stone size, or no stone matrix row, nothing is costed (HTTP 422) and the message names the designs and
variations and the missing metals and (size, code) pairs, grouped by design. With *Skip variations with
missing data* (`skip_incomplete`), the complete ones are costed and the others are listed in a separate
`NOT COSTED` section at the end of the CSV (and in the `X-Costing-Incomplete` response header); the
screen shows the same list. `POST /exports/costing-csv` takes `{variation_ids?, design_ids?, metal_code,
stone_code, gold_rate_per_g, setting_rate_per_pc, skip_incomplete?}` (`design_ids` means all the design's
variations); the older `{items: […]}` request still works. `POST /exports/costing-preview` takes the
same body and always answers 200 with the rows and the problems, for the screen.

**Save as costing sheet** keeps a result for later (**Costing sheets** in the top bar). A sheet stores the
inputs, every computed row, the totals, the wastage % per metal and the stone prices per matrix row that
were used, and the exact CSV. It is a snapshot (jsonb columns of the `costing_sheets` table, not
references to the masters), so changing the gold rate, wastage or a stone price afterwards never alters
an old sheet or its CSV. A sheet opens read-only with its CSV download. **Re-cost** makes a *new* sheet
from the same variations and metal with today's wastage and prices and a gold or setting rate you choose,
and shows the difference per variation; the old sheet is untouched. An admin may delete any sheet, a
designer their own. Creating, downloading, re-costing and deleting are in the audit log
(`costing_sheet.*`). The production role cannot reach any of this.

### Orders

to production. Not a customer order: no prices, payments or invoicing are stored. The screens are described under "Order screens" below.
the screens come later.

`POST /orders` takes `{metal_code, stone_code, items: [{variation_id, quantity}], name?, notes?, due_date?,
criteria?, place?}`. The selection is explicit; `criteria` (the filter the selection came from) is optional
and informational, and an order is created without it. Numbers are `ORD-0001`, `ORD-0002` … allocated in
the creating transaction from one locked counter row, so concurrent creates never repeat or skip one and a
failed create gives its number back. Up to 500 lines, quantity 1 to 100000, a variation once per order.

- **Snapshot.** Each line stores, when it is added, a copy of the design code / name / category / tags, the
  variation code, metal, stone code and specific gravity, the base input, net / stone / gross weight, stone %
  (stone weight as a percentage of gross weight), the full stone BOM, the CAD file names, SHA-256 hashes and sizes
  (3dm and stl) and the cover photo reference. The numbers come from the same code as `simulate-bom`, so they
  agree with it exactly. An order is read from its own tables only: editing the design, seats, a metal's SG or
  the stone matrix, soft-deleting or purging the design or variation never changes it (`design_id` /
  `variation_id` on a line become `null` after a purge; the snapshot stays whole). Storage keys are kept in a
  separate server-only column and are never sent to a client.
- **Missing data is never zero.** A variation whose stone data is missing for the chosen code (or that is
  deleted) makes the whole request fail with 422 and a `problems` list naming every such variation and the
  missing (size, code) pairs; nothing is created and no number is used.
- **Status.** `draft` → `placed` → `in_production` → `done`; `cancelled` from draft, placed or in production;
  `done` and `cancelled` are final. `POST /orders/:id/status {status, note?}` validates the move (409 if not
  allowed) and records who and when in the order's `history`. Orders are cancelled, never deleted (no delete
  route). Lines change only while the order is a draft (`PUT /orders/:id/lines`: a variation already on the
  order keeps its snapshot, a new one is snapshotted now); name, notes and due date change until the order is
  done or cancelled (`PUT /orders/:id`).
- **Clone / reorder.** `POST /orders/:id/clone {name?, metal_code?, stone_code?, skip_unavailable?}` makes a
  new draft from the old lines, re-snapshotted from the current designs, and returns `{order, diff}`; the diff
  lists per line `unchanged`, `changed` (field, old, new) or `unavailable`. Lines that can no longer be
  ordered fail the clone (422, listed) unless `skip_unavailable` is set, which drops and reports them.
- **Roles.** Everyone signed in may read (`GET /orders` with `status`, `q`, `from`, `to`, `limit`, `offset`,
  total in `X-Total-Count`; `GET /orders/:id`); admin and designer write. Creating, editing, changing lines,
  status changes and clones are in the audit log (`order.*`).
- **Live check.** `GET /orders/:id/live` (any role) compares every line's stored snapshot with a fresh one from
  the catalog today, in the line's own metal and stone code: `{lines: [{line_id, status, reason?, changes}]}` with
  `status` `unchanged`, `changed` (the fields, old and new) or `unavailable` (design or variation deleted or purged,
  stone data missing; `reason` says which). It only reads; the order is never touched.

#### Order screens

- **Make one.** In the **Catalog**, filter (category, tags, weights, metal, stone code, stone %), tick designs (the
  selection survives filter changes and paging; the bar shows the count) and choose **Create order**. The review
  step (`/orders/new`) lists every variation of the ticked designs as a line with a quantity (default 1) and a
  Remove button, starts with the catalog's current metal and stone code, and takes a title / buyer (free text), a
  due date and notes. **Save as draft** or **Create and place**. If the server refuses lines (missing stone data,
  a deleted design) it names them in its own words and **Leave out** moves them to a "Left out" list with the
  reasons. The catalog filter in use is stored with the order as an informational note.
- **Orders list** (`/orders`, everyone): number, title, status, metal · stone, lines, pieces, due date, creator;
  filter by status and text; cards on a phone.
- **One order** (`/orders/:id`): the lines as saved (net, stone, gross, stone %, stones, file names; the stone list
  folds open), the history of status changes with who and when, and, for admin and designer, the status buttons
  (each asks for an optional note; cancel says it cannot be reopened), **Order again** (a new draft from today's
  designs, then a list of what differs), and editing of title, due date and notes (until the order is done or
  cancelled) and of quantities and lines (draft only). A line whose design was edited since is marked **Changed
  since this order was made** with the fields; a deleted or purged one is marked **No longer in the catalog as
  ordered** with the reason; the snapshot is shown either way. Production sees all of it and none of the buttons.

#### What an order produces (stage 41)

All of these read the order's own snapshot, so they print the same after the design changes. Every role
may use the first four (production works from them; nothing in them is a price); costing is admin and
designer only.

- **Summary PDF**, `GET /orders/:id/summary.pdf`: order number and QR (a link to the order page when
  `APP_BASE_URL` is set, else the number), name, status, due date, metal, stone code, notes, the
  `criteria` note, totals (pieces, stones to set, net / stone / gross weight, average stone %; each is the
  sum of line weight x quantity), then the lines in a table that continues over as many pages as needed,
  repeating its header, with `Page n of N` in the footer. Same branding as the job bag.
- **Job bags**, `GET /orders/:id/job-bags.pdf[?per_piece=true]`: one bag per line, built from the snapshot
  (weights, BOM, base input, CAD file hashes) with the order number and quantity printed on it; with
  `per_piece=true` one bag for every piece ("piece 2 of 5"). At most 500 bags at once (400 otherwise).
  A variation purged since still prints from its snapshot.
- **CSV**, `GET /orders/:id/csv` (a row per line with the weights per piece and for the line, and a `TOTAL`
  row) and `GET /exports/orders-csv` (the orders list, same `status`, `q`, `from`, `to` filters, at most
  5000 rows). Text that came from people is neutralised against spreadsheet formulas like the other CSVs.
- **Cost this order**, `POST /orders/:id/costing {gold_rate_per_g, setting_rate_per_pc, skip_incomplete?,
  name?}` (metal and stone code come from the order). Costs the order's variations from **today's** master
  data and designs (wastage, stone prices, matrix, seats), multiplying every weight and amount by the line
  quantity (a `qty` column in the CSV; the sheet rows carry `quantity`), and saves an immutable costing
  sheet linked to the order (`costing_sheets.order_id`, migration 000019). Missing wastage, stone prices or
  matrix rows fail with 422 and the same grouped `problems` list as costing designs (nothing is saved, nothing
  is costed as zero), unless `skip_incomplete`. A line whose variation was purged cannot be costed and is
  reported as a problem. The answer is `{sheet, snapshot_differs, drift}`: `drift` lists, per line, how the
  live design differs from the order's snapshot, so a costing is never silently based on something other than
  what production will build. `GET /orders/:id/costing-sheets` lists the order's sheets; re-costing such a
  sheet keeps its quantities and its order.
- **Catalog**: the selection bar has **Print job bags** (any role): metal and stone code, optional *skip
  variations with missing stone data*, one PDF with a bag per variation of the selected designs (the existing
  `POST /exports/job-bags-pdf`, at most 200 bags).

### Downloading many CAD files at once

In the **Catalog**, tick designs (any role can; production too) and use **Download STL** or
**Download 3dm** in the bar at the bottom. A dialog first says how many files and how many megabytes
the ZIP holds and how many variations have no such file; **Download ZIP** then starts a normal browser
download that streams straight to disk (nothing is held in the page, so it works on a phone).

- The ZIP holds the **original files** (the vault stores them gzipped; they are unpacked while
  streaming), named `LR0001-V1.stl`. A name that clashes gets ` (2)`; names never contain a path.
- `MANIFEST.csv` is the last entry: design, variation, kind, name in the ZIP, uploaded file name, the
  recorded and the computed SHA-256, bytes, `status` (`ok`, `missing`, `hash-mismatch`, `error`) and a
  detail. A file whose bytes do not match the recorded checksum is flagged, never hidden; the ZIP's
  comment also warns when anything is not `ok`.
- Memory stays flat: each file goes through a 256 KB buffer, one at a time, and a closed browser tab
  stops the work. Limits: `CAD_ZIP_MAX_FILES` (1000) and `CAD_ZIP_MAX_MB` (5120) per ZIP (413 above
  them); one running download per user (429; per API instance when several run).
- API: `POST /api/v1/downloads/cad-zip` streams the ZIP for a script; the web app calls
  `POST /api/v1/downloads/cad-zip/link` (estimate plus a 2-minute signed link) because a browser cannot
  send the sign-in token with a plain download. Every download is audited as `download.cad_zip`.
- The package `backend/internal/ziparchive` is the reusable core; the order downloads below use it.

#### Order download and order package

On an order (any role may download):

- `GET /api/v1/orders/:id/download?files=stl|3dm|stl,3dm` streams one ZIP; `GET /api/v1/orders/:id/package?files=...`
  streams the same ZIP plus `ORD-0001-job-bags.pdf` (one job bag per line) and `ORD-0001-summary.csv`
  (one row per line: quantity, pcs, net/stone/gross g, stone %, file names and hashes; no prices exist on
  an order). Browsers use `.../download/link?files=...` or `.../package/link?files=...`, which return
  `files`, `missing`, `changed`, `bytes`, a `filename` and a 2-minute signed `url`
  (`/api/v1/downloads/order-zip/file?ticket=`, no bearer token) that saves the ZIP straight to disk.
- **The order's own copy ships.** The files come from the order snapshot (storage key, SHA-256 and size
  recorded when the order was made), not from the variation as it is now, and are verified against that
  hash while streaming. The job bags are drawn from the snapshot too (weights, BOM and stone code as
  agreed), so they cannot fail on a stone-weight row that changed later.
- `MANIFEST.csv` columns: `order, design, variation, metal, stone_code, quantity, kind, current_file`, then
  the usual `file_in_zip, original_name, expected_sha256, actual_sha256, expected_bytes, bytes_written,
  status, detail`. `current_file` says what the catalog holds now: `same`, `changed` (a different file,
  named in `detail`), `removed` (the variation has no file of that kind now), `variation-gone` (deleted or
  purged) or `none-when-ordered`. Anything but `same` is also counted in the ZIP comment and in `changed`
  of the link answer, so the shop floor knows the catalog moved on. Missing data is `status=missing`,
  never silently left out. The PDF and the summary are listed in the manifest as well.
- Same caps as the selection download (`CAD_ZIP_MAX_FILES`, `CAD_ZIP_MAX_MB`, counted from the recorded
  sizes; 413), one running download per user across both kinds (429), bounded memory (the job-bag PDF,
  at most 200 lines, is built before the first byte and held in memory), every run audited as
  `order.download` (order number, kinds, package, files, missing, changed, bytes).
- **Purged files and open orders.** The hourly purge never deletes a CAD file that an order which is not
  cancelled recorded: a replaced or removed file, or the files of a variation purged after
  `DELETED_FILE_RETENTION_DAYS`, stay in storage (listed on the Removed screen with their time up) and are
  deleted by the next purge after the last order holding them is cancelled. `done` orders keep holding
  their files, so the storage cost is the old versions of files that appear on an order that was not
  cancelled; cancel an order to release them. A file that is gone anyway (deleted by hand, a retention of
  0 before this feature) shows as `missing` with "the stored object is gone". The cover photo thumbnail is
  protected the same way for removed photos.

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
| `designer` | Create and edit designs, variations, seats and CAD files; costing export; create and change orders |
| `production` | Read designs, simulate the BOM, job-bag PDF, CAD downloads, read orders. Never sees wastage % or stone rates |

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

`TEST_DATABASE_URL` is only the *admin* connection. Every Go test package that uses the database gets
its own, made on demand by `internal/testdb` (`cad_portal_internal_service_test`,
`cad_portal_internal_handlers_test`, ...; the name comes from the package directory), so `go test ./...`
can run packages in parallel (`-p 8` included) without one package wiping the schema under another. A
database is only ever wiped if its name ends in `_test`. A missing database server fails the run when
`CI` is set and skips the database tests otherwise. A new package that needs the database calls
`testdb.Reset(t)` (or `testdb.DSN(t)`); do not hard-code a database name. The one thing this cannot
protect against is running `go test` twice at once against the same server: use separate servers
for that.

**Scale benchmark** (not part of the normal run). It seeds 5,000 designs / 15,000 variations / 60,000 seats, times a
full weight rebuild (as queued by a metal edit) and the catalog list queries:

```bash
cd backend
SCALE_TEST=1 SCALE_DESIGNS=5000 go test ./internal/service -run TestScaleRecalc -v -count=1
```

The rebuild is set-based: it locks the variations, reads them and all their seats in two queries, calculates in
Go with exactly the single-variation math, and writes the cache with three statements that skip unchanged rows.
At 15,000 variations that is about 3 seconds (it was about 100 seconds when done one variation at a time). A test
(`TestBulkRecalcMatchesPerVariation`) checks the two methods store identical values under several master-data
changes. The whole rebuild is one transaction: readers keep seeing the old complete weights until it commits,
and a failure leaves nothing half-written.

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

`scripts/e2e.sh` creates the two users with random passwords. It needs a fresh database (it
fails if the users already exist). With no arguments it runs everything in two steps: the `main`
Playwright project (the tests that create and change data: one worker, file order), then the `a11y`
project (the read-only accessibility scans, four workers in parallel, `E2E_A11Y_WORKERS` to change
that). With arguments it passes them to Playwright as they are, so a subset is:

```bash
scripts/e2e.sh smoke.zip                          # one file (the data from smoke.e2e.ts must exist)
scripts/e2e.sh --project=main --grep "weight matrix"
scripts/e2e.sh --project=main --headed            # watch it
scripts/e2e.sh --project=a11y --no-deps           # only the accessibility scans, on data already there
```

**Signing in.** The sign-in form is rate limited per address (10 a minute, burst 20), so the suite
does not use it in every test. `frontend/e2e/global-setup.ts` signs in the two E2E users once through
the API and `frontend/e2e/auth.ts` hands that saved session to each test (`signIn(page, email,
password)`, or `admin(page)` / `production(page)`). Tests of the form itself (login errors, forgot
password, idle sign-out) use `signInViaForm`. Do not write a new local `signIn` in a test file.
`scripts/ratelimit_test.sh` still tests the real limit.

On a CI failure the reports and traces are attached to the run as the `playwright-report` artifact.

The same run includes an accessibility pass (axe over every screen and dialog, light and dark, desktop
and 375 px) and the phone-layout checks; see [docs/accessibility.md](docs/accessibility.md).

## API documentation

`docs/api/openapi.yaml` (OpenAPI 3.0) describes every route, with request bodies, responses and the role
each needs. `docs/api/CadPortal.postman_collection.json` and `CadPortal.postman_environment.json` are
generated from it: import both into Postman, run **Auth > Sign in** once (it stores the token), then use any
request. After changing a route, update `openapi.yaml` and run `python3 docs/api/gen_postman.py` (needs
PyYAML). View the spec with any Swagger UI, for example
`docker run -p 8081:8080 -e SWAGGER_JSON=/api/openapi.yaml -v "$PWD/docs/api:/api" swaggerapi/swagger-ui`.

### How CI is laid out

Jobs that run in parallel, each on its own runner (see `.github/workflows/ci.yml`):

| Job | What it checks |
|---|---|
| `changes` | which of the others this change needs (required) |
| `test`, `sqlc`, `frontend`, `vulncheck` | Go tests, sqlc drift, Angular build and unit tests, advisories |
| `docker-stack` | compose files, deploy script, workflow lint, the production stack, Playwright (`main`, then `a11y`), sign-in rate limit |
| `docker-backup` | backup and restore round trip, the backup service (nightly dump and file copy) |
| `docker-multi` | two API instances on one empty database |
| `docker` | always runs and fails when any `docker-*` part failed, was cancelled or was skipped although needed; this is the one to require in branch protection |

The images are built with buildx and the GitHub Actions layer cache (scopes `api`, `web`, `backup`),
loaded into the runner and started with `--no-build`. The Playwright browser and the Yarn packages are
cached too. To run one part locally, run the scripts the job runs (for example
`scripts/multi_instance_test.sh`; set `CADPORTAL_NO_BUILD=1` to use images you already built, tagged
`<project>-api` and `<project>-web`).

## Regenerating sqlc code

sqlc is run through its official Docker image:

```bash
cd backend
docker run --rm -v "$PWD:/src" -w /src sqlc/sqlc:1.31.1 generate
```

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). Repository
protection settings are listed in [docs/github-settings.md](docs/github-settings.md).
