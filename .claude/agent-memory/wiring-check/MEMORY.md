# Beecon Wiring Audit Memory

## Known Issues (as of 2026-03-07, feat/cli-output-signals)

### ActionForbidden never produced
- `engine.ActionForbidden` (engine.go:42) is defined but never assigned to any ActionOutcome
- Apply loop falls through forbidden actions to `applyAction()` which returns a hard error
- The `case engine.ActionForbidden:` in commands.go:171 is unreachable
- Fix: add forbidden check before RequiresApproval check in Apply loop

### Engine.IsSimulated() is dead code
- Defined at engine.go:67, zero call sites
- commands.go uses res.Simulated instead (populated inline from e.exec.IsDryRun())

### Approve command missing simulation indicator
- Apply shows (simulated)/(LIVE) but approve does not
- Data is available on ApplyResult.Simulated

## Architecture Notes

### Executor interface (provider/executor.go)
- 3 methods: Apply, Observe, IsDryRun
- Only one implementation: DefaultExecutor (no mocks in tests)
- dryRun controlled by BEECON_EXECUTE env var

### CLI output (internal/cli/output.go)
- Writer struct with TTY-aware formatting
- Package-level `out` var in cmd/beecon/root.go, shared across commands.go
- Methods: Blank, Header, Line, ActionLine, NumberedAction, StatusLine, Summary, Next
- Symbol methods: OK, Fail, Warn, Arrow, Dot
- Color methods: Green, Yellow, Red, Bold, Dim

### API scrubbing (internal/api/server.go)
- scrubOutcomes() called in /api/resolve (apply) and /api/approve
- scrubActions() called in /api/resolve (plan) and /api/graph
- scrubNodes() called in /api/graph
- All use security.ScrubChanges / ScrubMap / ScrubStringMap

### Executor C0 helpers (provider/executor.go, as of Phase 1 deepening)
- parseIntIntent, parseBoolIntent, envFromIntent — used by RDS/Lambda/S3/ElastiCache handlers
- trustPolicyForService, detectTrustService — used by IAM handler
- parseSecurityGroupRules, serializeSGRules, sgRulesToIPPermissions, applySGRules — used by SG create/update + SG observe
- applyS3BucketConfig — used by S3 CREATE and UPDATE
- listAttachedPolicies — used by IAM UPDATE and DELETE
- validateAWSInput — called in applyAWS before handler dispatch (line 150)
- IAM managed_policies arn: validation is inside applyAWSIAM (line 642), NOT in validateAWSInput — so dry-run bypasses it

### detectRecordTarget gap: lambda not in SERVICE fallback
- detectRecordTarget handles lambda via LiveState["service"]=="lambda" (line 1735)
- But SERVICE fallback block (lines 1794-1813) does NOT include lambda
- If a resource has only IntentSnapshot with intent.runtime=lambda and no LiveState.service, it falls through to "generic"
- Practical impact: low, because after first apply LiveState will have service=lambda
