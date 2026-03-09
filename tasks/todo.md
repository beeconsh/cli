# Task Tracker

## Current Focus: Phase 5 — Multi-Cloud Parity (GCP)

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

### Phase 5.2: G3 — Resilience (in progress)
- [ ] Multi-step partial results for Cloud Run (deploy → IAM → traffic split)
- [ ] GCP error classification — expand `isGCPNotFound` with gRPC codes, googleapi errors
- [ ] Operation waiters for Cloud SQL, GKE creation (long-running operations)
- [ ] `withRetry` for transient GCP errors (503, rate limits) with exponential backoff

### Phase 5.3: G4 — Observation Depth (in progress)
- [ ] Deepen Cloud Run observation — revision, scaling config, env vars (scrubbed), service URL, IAM policy
- [ ] Deepen Cloud SQL observation — database_version, tier, storage_auto_resize, backup_config, ip_addresses
- [ ] Deepen Memorystore observation — redis_version, memory_size_gb, host, port, auth_enabled
- [ ] Deepen remaining GCP resource types — match AWS-level field extraction

### Phase 5.4: G2 — Stub Promotion (backlog)
- [ ] Cloud Functions (Lambda equivalent)
- [ ] GKE (EKS equivalent)
- [ ] Cloud CDN (CloudFront equivalent)
- [ ] Eventarc (EventBridge equivalent)
- [ ] API Gateway (API Gateway v2 equivalent)
- [ ] Identity Platform (Cognito equivalent)
- [ ] Cloud Monitoring standalone alarms (CloudWatch equivalent)

---

## Phase 3: Agent Interface Layer (remaining)

### Phase 3.2: Structured Output
- [x] Machine-parseable error format with error codes (CLIError struct)
- [ ] Document JSON schemas for agent developers

### Phase 3.3: Rich Plan Output
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

### Phase 5.5: Azure & General
- [ ] Azure resource-specific adapter depth
- [ ] Provider capability matrix command

### Phase 6: Trust & Governance
- [ ] Cost guardrails with auto-approve thresholds
- [ ] Dependency-weighted blast radius scoring
- [ ] Policy-based approval delegation
- [ ] Agent identity in audit trail

### Phase 7: Ecosystem
- [ ] Beecon modules / templates
- [ ] Remote state backend (S3/GCS/Azure Blob)
- [ ] Plugin SDK
