# CLAUDE.md — Internal Jewelry CAD Vault & Product Management Portal

## What this is
Internal manufacturing catalog, CAD/CAM file vault, and dynamic Job-Bag spec generator.
**Out of scope:** public storefront, customer payments, live spot-gold pricing. Don't build or suggest these.

## Environment
- **Go 1.23+**, **Angular CLI 22** (needs Node 22.22.3+ or 24.15+), and **Docker** are installed locally.
- **Local dev/test DB:** run Postgres 18 (alpine) in Docker (see `docker-compose.yml`). All automated tests run against this.
- **Shared testing Postgres on the server:** reached via `TEST_SERVER_DATABASE_URL` in `.env` (never commit credentials).
  - Read-only checks are fine. **Ask before** running migrations, seeds, `DROP`, `TRUNCATE`, or bulk writes against it.
- Secrets live in `.env` (gitignored); keep `.env.example` up to date.

## Repo layout
```
backend/            Go service (Gin, pgx, sqlc, golang-migrate)
  migrations/       000001_init_schema, 000002_seed_masters (up/down)
  internal/         handlers, services, db (sqlc output), storage (S3/R2)
frontend/           Angular 22 app (standalone components, Signals, zoneless, Vitest); package manager: Yarn 4 (`yarn.lock`, release pinned via `.yarnrc.yml`)
docker-compose.yml  Postgres 18 for local dev
docker-compose.prod.yml  production-style stack: db + api + web (nginx); images from backend/Dockerfile and frontend/Dockerfile
docker-compose.images.yml  overlay for prod: run the published ghcr.io images instead of building (see README, Releases)
deploy/             Caddyfile.example: TLS reverse proxy for going live
scripts/            backup.sh, restore.sh, deploy.sh, e2e.sh, ratelimit_test.sh, multi_instance_test.sh, backup_service_test.sh, deploy_test.sh and their tests (run in CI)
backend/cmd/backup  the backup service (own image, target `backup`): nightly encrypted database dump + incremental copy of files to a second bucket
```

## Stack decisions
- **Backend:** Gin REST API, `pgx/v5` + `sqlc`, `golang-migrate`.
- **Money and weights:** use `shopspring/decimal` in Go, never `float64`. Round only when storing (grams to 3 dp, carats to 4 dp).
- **Files:** `crypto/sha256` + `compress/gzip`, streamed (no full file in memory), stored in S3 or Cloudflare R2 behind role-protected presigned URLs.
- **Frontend:** Angular `ReactiveFormsModule` (`FormArray` for stone-seat rows), Signals `computed()` for live BOM math, Angular Material for tables and tag chips (decided; not PrimeNG).

## Domain model
**Taxonomy:** each Master Design has exactly one Category (Rings, Earrings, Pendants, Pendant Sets, Necklaces, Bangles, Bracelets) plus any number of Tags from an admin-controlled pool. The pool is grouped as Audience / Sub-Style / Motif / Attribute.

**Hierarchy:** Master Design (`LR001`) → CAD Variations (`LR001-V1`, `LR001-V2`). Each variation has:
- one base input (volume **or** weight + reference alloy)
- its CAD files
- its stone seats
- a pre-computed weight row per active metal

## Formulas (source of truth)
- Net weight from volume: `net_g = volume_mm3 × SG / 1000`
- Net weight from a reference weight: `net_target = net_ref × (SG_target / SG_ref)`
- Stone weight: `stone_g = total_carats × 0.200` (grams are the unit people enter and read; carats are what is stored; a weight per stone keeps 8 decimals of carats, totals are rounded to 4)
- Gross weight: `gross_g = net_g + total_carats × 0.200`
- Line carats: `stone_count × carats_per_stone`, where `carats_per_stone` comes from `stone_size_matrix(size_mm, stone_code)`

### Stone BOM resolution
- **CAD operator input:** enters only `size_mm` + `stone_count` per seat. A stone code is set only when a seat needs a lock (`locked_stone_code_id`).
- **Catalog cache:** flexible seats use the single `is_default_baseline` stone code (1HD), which gives `baseline_stone_carats` and `baseline_gross_weight_g`.
- **Job-bag / simulate-bom:** the planner picks a stone code. Flexible seats use that code; locked seats keep their own.
- **Missing matrix row:** if a size has no row for the chosen code, return an error that lists the missing (size, code) pairs. Never silently treat it as 0. The `v_seat_matrix_gaps` view finds these gaps ahead of time.

### Test fixtures (must pass)
Volume 200 mm³ gives these net weights:

| Metal | Net weight |
|---|---|
| 22KY (17.75) | 3.550 g |
| 18KY (15.58) | 3.116 g |
| 18KR (15.25) | 3.050 g |

The 2HD pointer values below are for tests only; real values come from Admin.

| Seat | Code | Pointer | Line carats |
|---|---|---|---|
| 24 × 1.10 mm | 2HD | 0.0070 | 0.1680 ct |
| 12 × 1.20 mm | 2HD | 0.0095 | 0.1140 ct |
| 1 × 2.50 mm | 2HD (locked) | 0.0650 | 0.0650 ct |

Expected totals: 37 pcs, 0.3470 ct (0.069 g), and 18KR gross 3.119 g.

## Database
- **Migrations are the schema source of truth.** Never edit an applied migration; add a new numbered one instead.
- **Key constraints already enforced:**
  - the base-input check (NULL-safe)
  - unique SHA-256 checksums, separately for 3dm and stl
  - only one default input metal and one default baseline stone code
  - a locked seat must match a `stone_size_matrix` row (composite FK, `ON DELETE SET NULL (locked_stone_code_id)`)
  - `updated_at` triggers
- **Weight cache rebuild:** `variation_metal_weights` and the baseline rollups on `cad_variations` must be recalculated whenever any of these change:
  1. a metal's SG
  2. stone matrix carats for the baseline code
  3. which code is the baseline
  4. a variation's base input or seats (do this one synchronously, in the same transaction)
- **Recalc jobs:** 1–3 enqueue a row in `weight_recalc_jobs`. A worker claims it with `FOR UPDATE SKIP LOCKED` and holds `pg_advisory_xact_lock` while running, so two recalcs never interleave. The full rebuild is set-based (two reads, three bulk writes, one transaction; the same Go math as the single-variation path, tested equal). Several API instances may run: see README, "Running more than one API instance".

## API (v1)
| Group | Endpoints |
|---|---|
| Audit log | `GET /api/v1/audit` (admin; filters `action` prefix, `user_id`, `from`, `to`; paging with `X-Total-Count`) |
| Auth & users | `POST /api/v1/auth/login`, `GET /api/v1/auth/me`, `POST /api/v1/auth/change-password`, `GET /api/v1/auth/config`, `POST /api/v1/auth/forgot-password` (public; same answer for every address), `POST /api/v1/auth/reset-password` (public; one-time token), `POST /api/v1/users/:id/reset-link` (admin; one-hour link, shown once), `GET/POST /api/v1/users`, `PUT /api/v1/users/:id`, `POST /api/v1/users/:id/password` |
| Masters | `GET /api/v1/masters/setup-status` (admin; what master data is still missing), `GET /api/v1/masters/export` (admin; zip of CSVs: alloys, stone codes, stone matrix, categories, tags), `GET/POST/PUT /api/v1/masters/categories[/:id]` (a prefix change restarts numbering for new designs), `GET/POST/PUT /api/v1/masters/tags[/:id]`, `GET/POST /api/v1/masters/metals`, `PUT /api/v1/masters/metals/:id`, `GET/POST /api/v1/masters/stone-codes`, `PUT /api/v1/masters/stone-codes/:id`, `GET/PUT/DELETE /api/v1/masters/stone-matrix`, `GET /api/v1/masters/matrix-gaps`, `GET /api/v1/masters/recalc-status` (writes admin only) |
| Undo | `GET /api/v1/trash` (editor; deleted designs, deleted variations and removed files), `POST /api/v1/trash/files/:id/restore`, `POST /api/v1/designs/:id/restore`, `POST /api/v1/variations/:id/restore` (refused while its design is deleted). Removed or replaced files and deleted variations are kept `DELETED_FILE_RETENTION_DAYS` (default 30) before an hourly purge deletes them (0 = at once). A deleted variation is a soft delete (`cad_variations.deleted_at`): hidden everywhere, code and CAD checksums stay reserved until purged |
| Designs | `POST/GET /api/v1/designs` (POST takes no code: it is assigned; `q` text search, `limit`/`offset` paging, total in `X-Total-Count`), `GET /api/v1/designs/:code`, `PUT/DELETE /api/v1/designs/:id` (soft delete; the code stays reserved), `POST /api/v1/designs/:id/duplicate` (editor; body `name?`, `variation_ids?` default all live, `copy_tags` default true; new design with the next automatic code, variations with the same base input and seats, never CAD files or photos; one transaction), `POST /api/v1/designs/import` (admin/designer; bulk create from CSV rows, all-or-nothing, `dry_run` validates only; never changes existing designs) |
| Variations & CAD vault | `POST /api/v1/designs/:id/variations`, `POST /api/v1/variations/:id/upload-cad`, `POST /api/v1/variations/:id/clone-seats`, `PUT /api/v1/variations/:id/seats`, `DELETE /api/v1/variations/:id`, `GET /api/v1/variations/:id/download`, `POST /api/v1/designs/:id/media` (editor; multipart `file`, optional `variation_id` BEFORE it; photo or video, max 5 per design or variation), `DELETE /api/v1/media/:id`, `PUT /api/v1/media/:id/cover` (media arrive with `GET /api/v1/designs/:code`), `DELETE /api/v1/variations/:id/cad?kind=3dm\|stl` (frees the checksum) |
| Backups | `GET /api/v1/backups` (admin; run history, stored dumps when the portal has a read-only token for the backup bucket), `POST /api/v1/backups/run` (admin; "Back up now": queues one request for the backup service, 409 if one is waiting or running), `GET /api/v1/backups/download?key=` (admin, audited; signed link to one encrypted dump, dumps only) |
| BOM & exports | `GET /api/v1/variations/:id/simulate-bom?code=2HD`, `POST /api/v1/exports/job-bag-pdf`, `POST /api/v1/exports/job-bags-pdf` (many bags, one PDF; max 200; `items` and/or `design_ids`, shared `metal_code`/`stone_code`; 422 lists every variation with missing stone data, `skip_incomplete` prints the rest and lists the skipped in `X-Skipped` and on a last page), `POST /api/v1/exports/costing-csv`, `GET /api/v1/exports/designs-csv` (any role; the import format, no wastage or rates) |

`GET /api/v1/designs` filter parameters: `category_id`, `tag_ids[]`, `metal_code`, `min_net_wt`, `max_net_wt`, `min_gross_wt`, `max_gross_wt`, `max_stone_cts`.

**Uploads:** hash the original bytes (not the gzipped bytes). Reject a duplicate with 409 and name the existing variation. Gzip while streaming to storage.

**Costing CSV:** net gold + wastage %, stone weight (grams and carats) and stone cost, and setting labour (pcs × rate). Stones are priced per piece by default (count × price per pc) per carat (carats × price per ct) or per gram (line grams × price per g, where line grams = unrounded line carats × 0.200), chosen per matrix row (`rate_basis` `pc`/`ct`/`g`; a `g` row must have `rate_per_g`). The CSV's last column `stone_pricing` says how each stone line was priced and the quantity priced. Stone weight in grams is carats × 0.200, shown wherever carats are (BOM lines at 4 dp, totals at 3 dp), including the job-bag PDF.

## Frontend screens
1. **Catalog + filter dashboard:** category, target purity, net/gross weight ranges, tags; cards show net/gross for the selected purity.
2. **CAD upload + stone-seat grid:** base input, 3dm/stl dropzones with SHA status, `FormArray` seat rows, clone seats from another variant.
3. **Design detail:**
   - full weight matrix across all metals
   - BOM simulator with a stone-code selector (live via Signals)
   - job-bag PDF and costing CSV export

## Open decisions (ask, don't guess)
- Wastage % per metal.
- Which alloy is the default input/reference.
- Real `stone_size_matrix` values (sizes, sieve, carats, rates).

## Decided
- Storage: Cloudflare R2 in production (bucket `cad-vault`, keys `cad/<design>/<variation>/<id>.<kind>.gz`); local disk (`STORAGE=local`) for development only.
- Design codes are automatic and sequential: category prefix (1 to 6 letters, set by an admin) plus a 4-digit number, e.g. `LR0001`, `LR0002`, counted per category. Nobody types a code: the API refuses one on create, and an import may only bring a code that is the category's prefix plus digits. A category without a prefix cannot take designs.
- Roles: `admin` (everything), `designer` (edit designs, seats, CAD, costing), `production` (read, job bags, CAD downloads; never sees wastage % or stone rates).

## How to work
- Build in stages, and stop for review after each:
  1. Masters + design/variation CRUD + weight calculation
  2. Seats + simulate-bom
  3. CAD upload/download
  4. Exports
  5. Angular screens 1 → 3
  6. Login (users, Argon2id, lockout) and a protected API
  7. Admin screens for master data (alloys, stone matrix with CSV import, categories, tags)
  8. Deployment: container images, production compose, health checks, CI image build and smoke test
  9. Catalog management: text search, paging, edit/delete designs, delete variations
  10. Audit log: who changed what (append-only, no request bodies), admin Audit screen, optional retention
  11. CAD file management: remove a wrong 3dm/stl file from a variation (deletes the stored object, frees its checksum)
  12. Backup and restore: scripts/backup.sh and scripts/restore.sh, with a round-trip test in CI
  13. Design import: bulk-create designs and variations from CSV (validate first, all-or-nothing)
  14. Design export: catalog CSV in the import format (round-trips into a fresh install)
  15. Release: tag-triggered publish of the api and web images to GHCR, and a compose overlay to run them
  16. Browser smoke tests: Playwright (frontend/e2e) against the production stack in the CI docker job
  17. Sign-in rate limit in the web container (per client address), tested in CI
  18. Automatic design codes (prefix + sequence) and the admin setup checklist (`GET /api/v1/masters/setup-status`)
  19. Stone weight in grams everywhere; master-data export (zip of CSVs); design media (images and video)
  20. Job-bag PDF carries the design photo
  21. Undo: deleted designs and removed or replaced files are recoverable (Removed screen, retention period)
  22. Stone weight in grams first; stone price per piece (default) or per carat
  23. Weight per stone kept to 8 decimals of carats; ready-made CZ stone weight list (docs/imports/cz-stone-weights.csv)
  24. Backups: nightly database dump (7 daily, 4 weekly, 12 monthly, one per year forever; age-encrypted) and incremental copy of all stored files to a second bucket, run history in `backup_runs`, setup-checklist warning when stale; photos and video stay un-gzipped on purpose
  25. Backups screen (admin): run history, "Back up now" (picked up by the backup service within a minute), list and download of encrypted dumps via a read-only token (`BACKUP_VIEW_*`)
  26. Phone-width layout: the top bar collapses into a Menu, wide tables scroll inside their own box, and a Playwright test asserts no screen scrolls sideways at 375 px
  27. Staging and production images: every green main commit is published as :staging (and :sha-<commit>); production runs only tagged versions (no :latest); scripts/deploy.sh pulls and restarts only the app containers, never the database
  28. Structured JSON logs with request ids (API and web), Docker log rotation, heartbeat pings for uptime (`HEARTBEAT_URL`) and for good backups (`BACKUP_HEARTBEAT_URL`)
  29. Forgotten passwords: one-hour single-use reset links (admin-made on the Users screen, or emailed when SMTP is configured), rate limited, audited
  31. Scale: the full weight rebuild is set-based (about 3 s at 15,000 variations instead of about 100 s; `TestScaleRecalc` with `SCALE_TEST=1`, equivalence test against the per-variation math); more than one API instance is safe (migration advisory lock, one heartbeat pinger via `maintenance_claims`, audit-retention try-lock, `SKIP LOCKED` everywhere else) and proven by `scripts/multi_instance_test.sh`
  33. Stone price per gram: a third `rate_basis` `g` with `rate_per_g` (migration 000014), line cost = line grams × price per gram from unrounded carats; `stone_pricing` column in the costing CSV; matrix CSV gains `rate_per_g`
  34. Duplicate a design (next automatic code, chosen variations with base input and seats, optional tags, never files) and restore deleted variations (soft delete, Removed screen, purge after the retention period)
  36. Job bag QR code (a link to the design page from `APP_BASE_URL` with `?v=<variation>`, else the plain variation code) and many job bags in one PDF (`POST /exports/job-bags-pdf`, "Print all job bags" / "Print selected" on the design page)
- Every service function that does weight/BOM math gets table-driven tests using the fixtures above.
- Run `go vet`, `go test ./...`, and `ng build` (plus `ng test --watch=false` in `frontend/`) before saying a stage is done.
