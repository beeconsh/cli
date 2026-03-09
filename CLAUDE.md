# CLAUDE.md — Beecon

## Project Overview

Beecon is an infrastructure-as-code tool designed for AI agent consumption. Agents generate `.beecon` files, plan changes with rich context (risk, cost, compliance), and apply them to AWS/GCP/Azure with built-in guardrails. Three output surfaces: CLI, REST API, and MCP (Model Context Protocol) server.

## Architecture

```
cmd/beecon/              Cobra CLI: 19 commands, global flags (--format, --profile, --debug)
  root.go                Entrypoint, flag registration, engine init, `beecon mcp` command
  commands.go            All command implementations with --format json support
internal/
  api/                   REST API: 15 endpoints, Bearer auth, CORS
  ast/                   Abstract syntax tree for .beecon files
  btest/                 Test assertion framework (.beecon-test files)
  classify/              Node type classification (SERVICE/STORE/NETWORK/COMPUTE)
  cli/                   Output formatting, color, JSON output, interactive prompts
  cloud/                 Cloud provider abstraction types
  compliance/            HIPAA/SOC2 framework enforcement with mutation pipeline
  cost/                  Budget parsing, pricing estimates, cheaper alternatives
  discovery/             Beacon file scanner (.beecon pattern matching)
  engine/                Core orchestrator: parse → plan → apply → drift → rollback
  ir/                    Intermediate representation (intent graph, profiles, edges)
  logging/               Debug logging (stderr, silent by default)
  mcp/                   MCP server: 13 tools over stdio for AI agent integration
  parser/                Regex-based DSL parser for .beecon files
  provider/              AWS/GCP/Azure executors, retry logic, target detection
  resolver/              Plan builder (topological sort, intent diff, boundary gates)
  scaffold/              Project initialization (init command)
  security/              Sensitive key registry, scrubbing functions, SafePath
  state/                 State store (file-locked JSON, transactions, schema migrations)
  ui/                    Mission Control web UI (embedded HTML/JS, 3-panel)
  wiring/                Cross-resource wiring (IAM, env vars, SG rules)
  witness/               Performance breach remediation suggestions
docs/
  INTERNALS.md           Implementation reference (architecture, control flow, data model)
  ROADMAP.md             Product roadmap (agent-first thesis, Phase 1-7)
tasks/
  todo.md                Task tracker with checkable items
  lessons.md             Lessons learned from QA rounds and corrections
```

## Key Technologies

- **Language**: Go 1.25
- **CLI Framework**: Cobra (spf13/cobra)
- **MCP SDK**: mcp-go v0.45.0 (mark3labs/mcp-go)
- **Cloud SDKs**: AWS SDK v2, GCP client libraries, Azure SDK for Go
- **State**: Local JSON file with file locking and transactional API
- **Testing**: Go stdlib `testing` package, table-driven tests
- **Build**: GoReleaser v2
- **CI**: GitHub Actions (test, vet, build on PR/push; GoReleaser on tag)
- **Deploy**: Homebrew tap (beeconsh/homebrew-beecon), auto-updated by GoReleaser

## Development Commands

```bash
# Build
go build -o beecon ./cmd/beecon

# Test (all 473 tests across 22 packages)
go test ./... -count=1

# Test with race detection (CI default)
go test -race -count=1 ./...

# Test specific package
go test ./internal/mcp/ -v -count=1

# Vet
go vet ./...

# Run locally
./beecon version
./beecon plan infra.beecon
./beecon mcp                    # Start MCP server on stdio

# Run with debug logging
./beecon --debug plan infra.beecon

# Run with profile
./beecon --profile production plan infra.beecon

# JSON output
./beecon plan --format json infra.beecon
```

## Release Workflow

**Releases are mandatory-tagged.** Every merge to main that ships user-facing changes MUST be tagged before the work is considered done.

```bash
# 1. Merge PR to main
# 2. Pull latest main
git checkout main && git pull

# 3. Tag the release (annotated tag with summary)
git tag -a v0.X.0 -m "Summary of what shipped"

# 4. Push the tag — triggers GoReleaser via GitHub Actions
git push origin v0.X.0
```

**What happens on tag push:**
1. GitHub Actions runs `release.yaml` → tests → GoReleaser
2. GoReleaser builds binaries (darwin/linux × amd64/arm64)
3. Creates GitHub Release with changelog
4. Pushes updated Homebrew formula to `beeconsh/homebrew-beecon`

**Version convention:** `v{major}.{minor}.{patch}` — bump minor for features, patch for fixes.

**Never skip tagging.** If code is merged to main, it gets a tag. The homebrew formula and GitHub releases are the distribution mechanism — untagged code is unreleased code.

## Engine Control Flow

```
beecon apply infra.beecon
  → Parse .beecon file (parser)
  → Build IR graph (ir)
  → Apply compliance mutations (compliance)
  → Wire cross-resource dependencies (wiring)
  → Load state via transaction (state)
  → Build plan from intent diff (resolver)
  → Annotate with boundary policy tags (resolver)
  → Estimate costs and check budget (cost)
  → Enrich plan: risk scores, rollback feasibility (engine)
  → Execute non-gated actions (provider)
  → Persist state updates and audit events (state)
  → Return result with cost report and compliance mutations
```

## State Mutation Pattern

All write operations use the transactional pattern:

```go
tx, err := e.store.LoadForUpdate()  // file lock + mutex
if err != nil { return nil, err }
defer tx.Rollback()                  // safe no-op if Commit called

// ... modify tx.State ...

if err := tx.Commit(); err != nil { return nil, err }
```

## Security Rules

1. **Never log or output secrets.** All output paths (CLI, API, MCP) must scrub sensitive data via `security.ScrubMap` / `ScrubStringMap` / `ScrubChanges`.
2. **Scrub consistently across all three surfaces.** When adding new output, match the API server's scrubbing pattern (see `internal/api/server.go:200-216`).
3. **SafePath on all file inputs.** Any handler accepting a file path from external input must call `security.SafePath(root, path)` before use.
4. **Timing-safe auth.** API key comparison uses `crypto/subtle.ConstantTimeCompare`.
5. **BeaconPath truncation.** Never expose full filesystem paths — use `filepath.Base()` on RunRecord.BeaconPath and ApprovalRequest.BeaconPath.

## Three-Surface Scrubbing Checklist

When adding new data to output, ensure all three surfaces scrub it:

| Data | Scrub |
|------|-------|
| ResourceRecord.IntentSnapshot | `ScrubMap` |
| ResourceRecord.LiveState | `ScrubMap` |
| ResourceRecord.Wiring.InferredEnvVars | `ScrubStringMap` |
| PlanAction.Changes | `ScrubChanges` |
| IntentNode.Intent / Env | `ScrubStringMap` |
| AuditEvent.Data | `ScrubMap` |
| RunRecord.BeaconPath | `filepath.Base` |
| ApprovalRequest.BeaconPath | `filepath.Base` |
| WiringResult.InferredEnvVars | Set to `nil` |

## MCP Server

The MCP server (`internal/mcp/server.go`) exposes 13 tools over stdio. Key constraints:

- **Concurrency:** mcp-go dispatches tools via goroutine pool. `Server.mu` mutex guards `ActiveProfile` mutation + engine call atomicity.
- **Error convention:** Tool errors use `mcp.NewToolResultError()` (isError response), never protocol errors (`return nil, err`).
- **Partial failure:** `handleApply` returns partial `ApplyResult` when `err != nil && res != nil` for orphaned resource recovery.

## Code Standards

- **Error handling:** Always wrap with context: `fmt.Errorf("operation failed: %w", err)`. Never swallow errors.
- **Naming:** Go stdlib conventions. Package names are single lowercase words. No stuttering (`state.StateStore` → `state.Store`).
- **Testing:** Table-driven tests with subtests. Test behavior, not implementation. Every change ships with tests.
- **Imports:** stdlib first, blank line, third-party, blank line, internal packages.

## Key Design Constraints

1. **`security` package cannot import `engine`, `state`, or `ir`** — the chain is `engine → resolver → security`. Type-specific scrub helpers stay local to each consumer.
2. **Engine methods take `context.Context` as first param** — enables cancellation, timeout propagation.
3. **Dry-run is the default** — live cloud execution requires `BEECON_EXECUTE=1` env var.
4. **State schema version must match** — newer state files are rejected to prevent corruption. Migrations run automatically on load.

## QA Checklist

```
[ ] All tests pass: go test ./... -count=1
[ ] Vet clean: go vet ./...
[ ] New code has tests
[ ] Sensitive data scrubbed on all three surfaces (CLI, API, MCP)
[ ] File path inputs validated with security.SafePath
[ ] No hardcoded secrets, ARNs, account IDs, or environment-specific values
[ ] Error handling on all external calls
[ ] MCP handlers return tool errors (isError), not protocol errors
```

## Quick Reference

```bash
go test ./... -count=1          # Full test suite
go test -race ./...             # Race detection (CI default)
go vet ./...                    # Static analysis
go build -o beecon ./cmd/beecon # Build binary
git tag -a v0.X.0 -m "msg"     # Tag release (MANDATORY after merge)
git push origin v0.X.0          # Trigger GoReleaser + Homebrew update
```
