# Beecon QA Engineer Memory

## Architecture
- Go CLI + HTTP API for infrastructure intent declaration/execution
- State stored in `.beecon/state.json` (JSON file, no database)
- Provider execution gated by `BEECON_EXECUTE=1` env var (dry-run by default)
- Three cloud providers: AWS, GCP, Azure with per-service adapters in `internal/provider/`
- Engine orchestrates parse -> plan -> apply flow in `internal/engine/engine.go`
- API server in `internal/api/server.go` with opt-in API key middleware (BEECON_API_KEY)
- State store: atomic writes via tmp+rename, 0600 perms, transactional LoadForUpdate/Commit/Rollback

## Key Files
- `internal/provider/executor.go` - AWS apply/observe (~1630 lines)
- `internal/provider/gcp.go` - GCP apply/observe
- `internal/provider/azure.go` - Azure apply/observe
- `internal/provider/provider.go` - Cloud connectivity (Connect command)
- `internal/engine/engine.go` - Core engine (Apply, Plan, Approve, Rollback, Drift)
- `internal/api/server.go` - HTTP API (14 endpoints, API key middleware)
- `internal/state/state.go` - File-based state persistence with transactions
- `internal/security/redact.go` - Canonical sensitive key registry + scrub functions
- `internal/resolver/plan.go` - Plan builder with diffIntent
- `internal/ir/ir.go` - Intent graph IR, Snapshot() for intent hashing

## Round 2 Fixes (verified working)
- Transactional state (LoadForUpdate/Commit/Rollback) for TOCTOU
- Canonical security package eliminating duplicate key lists
- Timing-safe API key comparison (crypto/subtle)
- Approval integrity with sha256 intent hash
- Audit cap at 10000, context propagation, rollback cloud calls
- isNotFound uses smithy SDK error codes
- diffIntent scrubs sensitive keys

## Critical Round 3 Finding: Scrubbed IntentSnapshot Corrupts Rollback
- engine.go:645 stores `rec.IntentSnapshot = security.ScrubMap(snap)` (scrubbed at rest)
- engine.go:747,779 rollback uses `copyMap(rec.IntentSnapshot)` as Intent for cloud API calls
- Result: rollback sends "**REDACTED**" as literal values to cloud APIs
- Fix: store unscrubbed IntentSnapshot, scrub only at API boundary

## Security Patterns
- IntentSnapshot now stored unscrubbed (Round 3 fix); scrubbed at API boundary only
- API endpoints scrub at response boundary (server.go:189-202, 326-328)
- API /api/state scrubs IntentSnapshot, LiveState, Changes, AuditEvent.Data, BeaconPath
- UI embeds API key in HTML meta tag (html.EscapeString used); UI NOT behind API key middleware
- safePath() used on all path-accepting endpoints EXCEPT /api/apply (Phase 4 bug)

## Phase 4 QA Findings (Round 4)
- P0: /api/apply missing safePath() — path traversal to read/execute arbitrary beacon files
- P0: UI esc() missing quote escaping — XSS in onclick handlers via approval IDs
- P1: CLI --format json (status, apply, plan, drift) bypasses ALL scrubbing — leaks secrets
- P1: UI serves API key in unauthenticated HTML (no middleware on / route)
- P3: withRetry (retry.go) is dead code — never called in production Apply/Observe
- Scrub helpers (scrubOutcomes, scrubActions, scrubNodes) are in api package — need to move to security for CLI reuse

## Concurrency Pattern
- Mutating: LoadForUpdate -> expireApprovalsInline -> modify -> Commit (or defer Rollback)
- Read-only: runExpireApprovals (own tx) -> Load (separate)
- No inter-process locking (file-level flock) for multiple CLI instances
