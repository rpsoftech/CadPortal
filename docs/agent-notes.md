# Notes for automated assistants and new contributors

Working knowledge about this repository that is not obvious from the code. `CLAUDE.md` stays the source of truth
for the domain model, formulas and stages; this page covers how to work here.

## Working rules

- Commit messages, code comments, docs and pull request descriptions do not name the tool that wrote the change.
  Commits carry the owner's identity and no trailer lines.
- Develop on the branch you were given. After a squash merge the remote branch still holds the old commits, and a
  force-push is not allowed. Restart from `origin/main`, then run `git merge -s ours origin/<branch>` (the tree must
  not change) and push normally.
- Do not open a pull request unless asked. When asked, fill in `.github/pull_request_template.md` and say honestly
  what could not be run locally.
- Review every pull request once it is ready. For one you did not write, review without approving or merging unless
  asked. Dependabot pull requests may be merged when CI is green.
- Ask before migrations, seeds, `DROP`, `TRUNCATE` or bulk writes on the shared Postgres. Never edit an applied
  migration. Money and weights are decimals, never floats. Never commit secrets.

## Checking a change locally (no Docker needed)

- **Go:** from `backend/`, set `GOTOOLCHAIN=go1.27.4` (go.mod needs it) and `TEST_DATABASE_URL` to a local Postgres,
  then `gofmt -l . && go vet ./... && go test -count=1 -p 1 ./...`. `govulncheck` needs `vuln.go.dev`, so CI is
  where it runs.
- **sqlc:** build sqlc 1.31.1 with `go install`, run `sqlc generate` in `backend/`; a clean tree must show no diff.
- **Frontend:** needs Node 22.22.3 or newer. Use `yarn install --immutable`, then `ng build` and
  `ng test --watch=false`. The repo's `.yarnrc.yml` names a private registry; override it with
  `YARN_NPM_REGISTRY_SERVER=https://registry.npmjs.org` when it is unreachable.
- **API docs:** `python3 docs/api/gen_postman.py --check`, validate the spec with `openapi-spec-validator`, and
  `bash .github/scripts/changes_test.sh`.
- Go's test cache does not see `docs/api/openapi.yaml` (it is outside the module). Use `-count=1`, as CI does.

## Things that surprised people

- **Every route must be in `docs/api/openapi.yaml`.** The `test` job compares the real router with the spec in both
  directions. After editing the spec run `python3 docs/api/gen_postman.py`.
- **Sessions.** Every login is a row in `sessions` and the token carries its id (`sid`). A test that mints a token
  with `auth.Issue` has no session and gets 401 when the session check is wired; log in, or use `IssueSession`.
  Signed download tickets use `Validate` only, so they need no session.
- **The e2e suite shares one saved login per user.** A test that signs out through the UI would end it for every
  other test.
- **Test image pinning.** `scripts/backup_service_test.sh` pins RustFS by digest because `latest` changed behaviour.
  Dependabot does not watch it, the Go version in `go.mod`, or the golang image tag; see `docs/dependabot.md`.
- **A red Dependabot pull request is not always its fault.** Read the log first (a new advisory, a floating image,
  or a tool that needs a matching release).
- **Many Dependabot pull requests at once** conflict on lockfiles. Apply them together in one pull request and
  Dependabot closes its own.
- `docs/imports/cz-stone-weights.csv` must keep empty prices (a test asserts it). The priced STAR list is a separate
  file.

## Decisions already made

- Roles: admin (everything), designer (edit), production (read; never sees wastage or rates).
- Wastage: 22KY 6, 20KY 5.5, 18KY 6, 18KR 7. An admin enters it under Master data; no migration sets it.
- STAR stone price: 0.55 per piece for 1.00 to 1.50 mm.
- Idle limit: `IDLE_TIMEOUT_MINUTES` (default 30) plus 2 minutes of grace; `SESSION_HOURS` (default 8) is the cap.

## Still open (owner decides)

- The default input/reference alloy, the specific gravity of 20KY, the real stone matrix values, and a weight for
  0.9 mm STAR.
- Merging the AWS SDK update (Dependabot #86): it changes the default retry behaviour, so upload and download one
  real file on staging first.
- Go-live checks: first nightly backup, a restore, the Backups screen, uptime monitors, an SMTP test, a real version
  tag, and the production server with TLS.
