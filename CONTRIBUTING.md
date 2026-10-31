# Contributing

Thanks for your interest. This repository is source-available under the
[CadPortal Personal Use License](LICENSE), and contributions are welcome at the owner's
discretion.

## Ground rules

- By submitting a contribution you confirm you wrote it (or have the right to submit it)
  and you grant the copyright holder a perpetual, worldwide, irrevocable license to use,
  modify and relicense it as part of the project, including commercially.
- Read [CLAUDE.md](CLAUDE.md) first: it holds the domain rules, formulas, test fixtures
  and the list of still-open decisions. Do not guess at the open decisions (wastage %,
  default alloy, real stone matrix values, category prefixes, storage naming).
- **Never commit secrets.** Use `.env` (gitignored) and keep `.env.example` current.

## Workflow

1. Open an issue before large changes.
2. Branch from `main`; `main` is protected, so changes land by pull request.
3. Keep pull requests small and focused.
4. Before pushing, run from `backend/`:

   ```bash
   gofmt -l .        # must print nothing
   go vet ./...
   go test ./...
   ```

   and from `frontend/`: `yarn ng test --watch=false` and `yarn ng build`.

## CI

CI runs only the jobs a change needs (`.github/scripts/changes.sh`):

| Changed files | Jobs that run |
|---|---|
| `backend/**` or `docs/api/**` | `test` (for `docs/api/**` that includes the API-docs checks), plus `vulncheck` for non-test Go code or `go.mod`/`go.sum`, `sqlc` for queries, migrations or `sqlc.yaml` |
| `frontend/**` | `frontend` |
| image sources (non-test files in `backend/` or `frontend/`) or `docker-compose.prod.yml` | `docker` (parts `docker-stack`, `docker-backup`, `docker-multi`) |
| other docs, README, `.github/dependabot.yml`, the dev `docker-compose.yml` | none |
| `.github/workflows/ci.yml` or `.github/scripts/` | everything |

Manual runs, the weekly scheduled run, and any push or PR where the base commit cannot be
found also run everything. A skipped job shows as "skipped", which counts as passing for the
required checks; the always-running `changes` job is itself required, so a broken change
detector blocks the merge instead of silently skipping the tests. After editing the
detector, run `bash .github/scripts/changes_test.sh`.

## Code conventions

- Money and weights use `shopspring/decimal`, never `float64`. Round only when storing
  (grams to 3 dp, carats to 4 dp).
- Any function doing weight, BOM or costing math needs table-driven tests, including the
  fixtures in `CLAUDE.md`.
- Never edit an applied migration. Add a new numbered one.
- Edit SQL in `backend/internal/db/queries/` and regenerate with sqlc; do not hand-edit
  generated files.
- A missing matrix row or rate is an error that names what is missing, never zero.
