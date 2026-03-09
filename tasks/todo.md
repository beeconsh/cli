# Task Tracker

## Current Focus: Phase 3 — Agent Interface Layer

### Phase 3.1: MCP Server
- [x] Design MCP tool schema (tools, inputs, outputs)
- [x] Implement MCP server entrypoint (`beecon mcp` command, stdio transport)
- [x] Expose 13 tools: validate_beacon, plan, apply, show_status, detect_drift, approve, reject, rollback, list_runs, list_approvals, get_history, discover_beacons, connect_provider
- [x] Tool error handling (isError responses, not protocol errors)
- [x] Security scrubbing on all MCP output paths
- [x] MCP server tests (input validation + happy path + scrubbing)
- [x] QA Round 4: Fix 7 HIGH findings (path traversal, scrub parity, ActiveProfile race, partial failure, CLI nil guards)
- [ ] Integration tests with MCP client (end-to-end stdio)
- [ ] Tool discovery/capability introspection (toolset grouping)

### Phase 3.2: Complete Structured JSON Output
- [x] Audit all CLI commands for `--format json` coverage
- [x] Add JSON to 8 commands: validate, approve, reject, history, rollback, refresh, import, connect
- [x] Fix resolver.Plan JSON tag (Actions → actions)
- [ ] Machine-parseable error format with error codes (CLIError struct)
- [ ] Document JSON schemas for agent developers

### Phase 3.3: Rich Plan Output
- [x] Add risk scoring per action (1-10 scale, low/medium/high/critical levels)
- [x] Add rollback feasibility per action (safe/risky/impossible)
- [x] Add cost-per-action (joined from CostReport.Estimates)
- [x] Add compliance mutations count per action
- [x] Add PlanSummary aggregate (counts, risk, cost, budget remaining)
- [ ] Add cost delta (current monthly vs proposed monthly)
- [ ] Add dependency chain depth metrics

---

## Backlog

See `docs/ROADMAP.md` for full Phase 4-7 details.

### Phase 4: Agent Autonomy
- [ ] Idempotent apply (safe retry after partial failure)
- [ ] `beecon diff` command (beacon vs state comparison)
- [ ] Structured error recovery guidance
- [ ] Self-healing drift (`drift --reconcile`)

### Phase 5: Multi-Cloud Parity — GCP to AWS Parity

#### G1: Wiring Layer (highest leverage)
- [ ] `gcpIAMActionsFor()` — IAM role inference for GCP dependency pairs
- [ ] `gcpInferEnvVars()` — auto-inject Cloud SQL connection strings, Secret Manager names, Pub/Sub topics
- [ ] `gcpInferFirewallRules()` — firewall rules from IR graph edges
- [ ] Cloud Monitoring alarms (post-apply `alarm_on` for Cloud Run, Cloud SQL, Memorystore, Compute)
- [ ] Cloud Logging retention (post-apply `log_retention` for Cloud Run, Cloud Functions)

#### G2: Stub Promotion (7 generic → resource-specific)
- [ ] Cloud Functions (Lambda equivalent)
- [ ] GKE (EKS equivalent)
- [ ] Cloud CDN (CloudFront equivalent)
- [ ] Eventarc (EventBridge equivalent)
- [ ] API Gateway (API Gateway v2 equivalent)
- [ ] Identity Platform (Cognito equivalent)
- [ ] Cloud Monitoring standalone alarms (CloudWatch equivalent)

#### G3: Resilience
- [ ] Multi-step partial results for Cloud Run
- [ ] GCP error classification (gRPC codes.NotFound, googleapi 404)
- [ ] Operation waiters (Cloud SQL, GKE creation)
- [ ] withRetry for transient GCP errors

#### G4: Observation Depth
- [ ] Deepen Cloud Run, Cloud SQL, Memorystore observation
- [ ] Deepen remaining 9 resource types
- [ ] Add observation for promoted stubs

#### Azure & General
- [ ] Azure resource-specific adapter depth
- [ ] Provider capability matrix command

### Phase 6: Trust & Governance
- [ ] Cost guardrails with auto-approve thresholds
- [ ] Blast radius scoring
- [ ] Policy-based approval delegation
- [ ] Agent identity in audit trail

### Phase 7: Ecosystem
- [ ] Beecon modules / templates
- [ ] Remote state backend (S3/GCS/Azure Blob)
- [ ] Plugin SDK
