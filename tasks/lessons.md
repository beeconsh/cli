# Lessons Learned

## Architecture
- Transactional state API (LoadForUpdate/Commit/Rollback) was the right abstraction — it already supports swapping to remote backends without touching engine code
- File locking with flock is process-scoped and advisory on NFS — fine for single-machine agent use, not for multi-machine teams
- Approval integrity via SHA-256 beacon hash prevents TOCTOU but doesn't prevent duplicate approvals from concurrent processes

## QA Patterns
- Round 1: Security hardening catches (credential scrubbing gaps, timing-safe auth)
- Round 2: Transactional state, context propagation, canonical security package
- Round 3: Partial result on multi-step failure, cross-cutting alarm dimensions, sensitive key expansion
- Round 4: Critical/high/medium findings across engine, parser, API, provider layers
- Round 5 (Phase 3 QA): 7 HIGH findings — three-surface scrubbing parity gap, path traversal, ActiveProfile race, partial failure discard, CLI nil deref
- Pattern: QA rounds find diminishing but still important issues — always worth running
- When adding a new output surface (MCP alongside API/CLI), audit the gold-standard surface (API) and replicate its scrubbing exactly — don't rewrite from scratch
- Circular dependency check: security → engine is blocked by the chain engine → resolver → security. Use new packages or keep scrub helpers local when types cross package boundaries

## Product Direction
- Remote state is a Phase 7 concern, not Phase 3 — the agent-first thesis means single-machine state is sufficient for v1
- MCP server integration is the highest-leverage Phase 3 item — transforms Beecon from scriptable CLI to native agent tool
- Plan output richness directly correlates with agent autonomy — richer plans = fewer human escalations
