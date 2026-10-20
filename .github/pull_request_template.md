## What and why

<!-- What does this change, and why? Link the issue. -->

## Checklist

- [ ] `gofmt -l .`, `go vet ./...` and `go test ./...` pass locally (from `backend/`)
- [ ] Weight/BOM/costing math has table-driven tests using the `CLAUDE.md` fixtures
- [ ] No secrets, tokens or real credentials in the diff
- [ ] Schema changes are a **new** migration (no edits to applied ones)
- [ ] SQL changes were regenerated with sqlc
- [ ] I did not guess at any open decision listed in `CLAUDE.md`
- [ ] I agree my contribution is licensed as described in `CONTRIBUTING.md`
