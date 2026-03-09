# Task Tracker

## Completed: Phase 5 — Multi-Cloud Parity (GCP) ✅

### Phase 5.1: G1 — Wiring Layer ✅ (PR #27, merged)
- [x] `GCPIAMRolesFor()` — 24-entry role matrix for GCP dependency pairs
- [x] `InferGCPEnvVars()` — Cloud SQL, Memorystore, GCS, Pub/Sub, Secret Manager, Cloud Run, Cloud Functions
- [x] `InferGCPFirewallRules()` — firewall rules from IR graph edges (VPC-resident only)
- [x] Cloud Monitoring alarms — `gcpAlarmMetricForTarget` with 20 mappings across 6 targets
- [x] Cloud Logging retention — `log_retention` for Cloud Run, Cloud Functions
- [x] Unified `detectGCPTarget` → `classify.ClassifyGCPNode` delegation
- [x] QA: 6 findings fixed (P1 classification sync, P2 Cloud Run VPC, P2 metric validation, P3 port bounds, P3 DRY fieldVal)
- [x] Structured logging across 10+ packages
- [x] 562 tests, 22 packages green

### Phase 5.2: G3 — Resilience ✅ (PR #28, merged)
- [x] `isGCPNotFound()` — gRPC codes, googleapi errors, storage-specific errors
- [x] `isGCPTransient()` — 503, rate limits, gRPC Unavailable/ResourceExhausted
- [x] `withGCPRetry()` — exponential backoff (500ms/1s/2s) + jitter, max 3 retries
- [x] Cloud Run multi-step partial results on mid-step failure
- [x] Replaced all 25+ `isNotFound(err)` string-match calls
- [x] 16 not-found test cases, 17 transient cases, 5 retry behavior tests
- [x] QA: 3 findings fixed (consistent isGCPNotFound, SecretManager partial results, retry safety)

### Phase 5.3: G4 — Observation Depth ✅ (PR #29, merged)
- [x] Deepen Cloud Run observation — revision, scaling config, env vars (scrubbed), service URL, IAM policy
- [x] Deepen Cloud SQL observation — database_version, tier, storage_auto_resize, backup_config, ip_addresses
- [x] Deepen Memorystore observation — redis_version, memory_size_gb, host, port, auth_enabled
- [x] Deepen remaining GCP resource types — match AWS-level field extraction
- [x] 284+ lines of observation tests
- [x] QA: P1 `fmt.Sprint(nil)` → `intentString` helper, Subnet region default, generic observe fix

### Phase 5.4: G2 — Stub Promotion ✅ (PRs #31, #32, #34, merged)
- [x] Cloud Functions — real CREATE/UPDATE/DELETE via Cloud Functions v2 API + deep observe
- [x] GKE — cluster lifecycle via Container API + deep observe
- [x] Cloud CDN — backend service with CDN policy + deep observe
- [x] Cloud Monitoring — AlertPolicy lifecycle + deep observe
- [x] Eventarc — trigger lifecycle with event filters + deep observe
- [x] API Gateway — multi-step lifecycle (API + Config + Gateway) + partial results
- [x] Identity Platform — tenant-based lifecycle + phone number scrubbing
- [x] QA: 8+ findings fixed (ProviderID doubling, retry on UPDATE, observe name derivation, SERVICE routing)
- [x] 888 tests, 22 packages green

---

## Completed: Phase 4 — Agent Autonomy ✅

### Phase 4.1: Idempotent Apply ✅ (PR #35, merged)
- [x] Re-running apply on already-applied state is a safe no-op
- [x] Partial failure recovery via CompletedActions tracking on RunRecord
- [x] CREATE skips when ProviderID exists, DELETE skips when already removed
- [x] QA: P1 completedActionKey collision fixed (NodeID not NodeName), P2 CLI skipped action display

### Phase 4.2: `beecon diff` ✅ (PR #36, merged)
- [x] Compare beacon file vs current state without full plan cycle
- [x] Added/removed/modified resources with field-level diffs
- [x] Supports --format json for agent consumption
- [x] QA: P1 sensitive field scrubbing in diff output, P2 reflect.DeepEqual for comparison

### Phase 4.3: Structured Error Recovery ✅ (via PR #30)
- [x] CLIError with error taxonomy: auth, quota, conflict, transient, validation
- [x] Recovery hints per error type
- [x] Retry-safe flag on transient errors

### Phase 4.4: Self-Healing Drift ✅ (PR #37, merged)
- [x] `beecon drift --reconcile` auto-generates a fix plan
- [x] `beecon drift --reconcile --apply` auto-fixes in one step
- [x] Reconciliation report with per-resource status
- [x] QA: P1 boundary policy enforcement, P1 CREATE for cloud-deleted resources

### Stats: 933 tests, 22 packages, all green

---

## Completed: Phase 3 — Agent Interface Layer ✅

### Phase 3.1: MCP Server ✅
- [x] 13 MCP tools over stdio
- [x] Tool annotations (ReadOnly, Destructive, Idempotent)
- [x] Security: path traversal, scrubbing parity, ActiveProfile mutex
- [x] Partial failure: handleApply returns partial ApplyResult
- [x] QA Round 5: 7 HIGH findings fixed
- [ ] Integration tests with MCP client (end-to-end stdio)
- [ ] Tool discovery/capability introspection

### Phase 3.2: Structured Output ✅ (PR #30, merged)
- [x] Machine-parseable error format with CLIError struct
- [x] 9 error categories, 15 error codes
- [x] Recovery hints and retry-safe flags
- [x] JSON and text error output in root.go
- [ ] Document JSON schemas for agent developers

### Phase 3.3: Rich Plan Output
- [x] Risk scoring, rollback feasibility, cost-per-action, compliance mutations, PlanSummary
- [ ] Cost delta (current monthly vs proposed monthly)
- [ ] Dependency chain depth metrics

---

## Backlog

See `docs/ROADMAP.md` for full Phase 5-7 details.

### Phase 5.5: Azure Adapter Depth + Provider Capability Matrix ✅

#### 5.5A: Azure Wiring Layer ✅ (PR #38, merged)
- [x] `ClassifyAzureNode()` — 18 Azure target types across STORE/NETWORK/SERVICE/COMPUTE
- [x] `AzureIAMRolesFor()` + RBAC role matrix — 30+ entries covering 13 target types
- [x] `InferAzureEnvVars()` — 8 target types (postgres_flexible, mysql_flexible, azure_cache_redis, blob_storage, key_vault_secret, service_bus, container_apps, functions)
- [x] `InferAzureNSGRules()` — NSG rule inference following GCP firewall pattern
- [x] Azure dispatch in WireGraph — classify, IAM, env, NSG, topology (5 switch points)
- [x] QA: 10 findings, 7 HIGH/MEDIUM fixed (Redis role, functions role, missing targets, nil-map guard, COMPUTE parity)

#### 5.5B: Azure Resilience + Deepened Observation ✅ (PR #40, pending)
- [x] `isAzureNotFound()` — azcore 404, error codes (ResourceNotFound, ResourceGroupNotFound), string fallback
- [x] `isAzureTransient()` — 429/500/502/503/504, ServerBusy/TooManyRequests, context.DeadlineExceeded, string fallback
- [x] `withAzureRetry()` — exponential backoff (500ms/1s/2s) + jitter, max 3 retries
- [x] Migrated all `azureStatusCode(err) == 404` → `isAzureNotFound(err)` (6 adapters)
- [x] Deepened observation: blob_storage, key_vault_secret, vnet, subnet, nsg, managed_identity
- [x] 5 test functions: isAzureNotFound (13 cases), isAzureTransient (14 cases), consistency, withAzureRetry (5 subtests), deprecated compat

#### 5.5D: Provider Capability Matrix ✅ (PR #39, merged)
- [x] `beecon providers` CLI command with `--format json` and `--provider` filter
- [x] `GetProviderCapabilities()` / `GetAllProviderCapabilities()` — introspection API
- [x] Real vs generic adapter classification (AWS: 22, GCP: 19, Azure: 8 real adapters)
- [x] Deep observe tracking, wiring coverage per target
- [x] 8 test functions with JSON roundtrip, sorted output, tier validation
- [x] QA: 4 findings, 2 HIGH fixed (false observe claims for 4 AWS targets, missing Azure wiring targets)

### Phase 6: Trust & Governance
- [ ] Cost guardrails with auto-approve thresholds
- [ ] Dependency-weighted blast radius scoring
- [ ] Policy-based approval delegation
- [ ] Agent identity in audit trail

### Phase 7: Ecosystem
- [ ] Beecon modules / templates
- [ ] Remote state backend (S3/GCS/Azure Blob)
- [ ] Plugin SDK
