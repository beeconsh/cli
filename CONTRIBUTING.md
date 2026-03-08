# Contributing to Beecon

## Getting Started

```bash
git clone https://github.com/beeconsh/cli.git
cd cli
go test ./...
go vet ./...
```

## Development Workflow

1. Create a branch from `main`
2. Make changes with tests
3. Run `go test -race ./...` and `go vet ./...`
4. Open a PR against `main`

## Code Standards

- All changes ship with tests
- Use the transactional state API (`LoadForUpdate`/`Commit`/`Rollback`) for mutations
- Scrub sensitive fields via `security.ScrubMap`/`ScrubStringMap`/`ScrubChanges` at every output boundary
- Add new sensitive key patterns to `internal/security/redact.go` (single source of truth)
- Use `context.Context` as the first parameter for all public engine methods
- Provider executor validates inputs before making SDK calls

## Architecture

See `docs/INTERNALS.md` for package layout, control flow, and implementation details.

See `UPDATED_DESIGN.md` for the design document mapping implementation to original intent.
