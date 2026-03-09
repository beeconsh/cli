# Beecon Roadmap

## Product Thesis

Beecon is **the infrastructure language your agent uses to deploy to the cloud**. The roadmap is shaped entirely around making Beecon the best possible infrastructure tool *from an AI agent's perspective*.

The agent workflow:
1. Generate/edit `.beecon` files
2. Validate and plan
3. Reason about the plan (cost, risk, compliance)
4. Apply with appropriate guardrails
5. Monitor and self-heal

Every friction point in that loop — ambiguous output, missing context, inability to recover, needing human intervention — is a roadmap item.

---

## Completed

### Phase 1: Core Engine
- [x] `.beecon` language parser and semantic validation
- [x] AST to provider-agnostic intent graph (IR) with profile inheritance
- [x] Resolver planning from intent vs state diff (CREATE/UPDATE/DELETE/FORBIDDEN)
- [x] Cross-resource wiring: IAM policies, env vars, security group rules from `needs`
- [x] Compliance enforcement: HIPAA/SOC2 framework mutation pipeline
- [x] Cost governance: budget parsing, estimates, cheaper alternatives
- [x] Local persistent state with transactional API (LoadForUpdate/Commit/Rollback)
- [x] Execution engine: apply, approve, reject, rollback, drift, refresh, import
- [x] Provider retry with exponential backoff and jitter
- [x] Test assertion framework (`.beecon-test` files)
- [x] Continuous drift monitoring (`watch` command)
- [x] CLI: 19 commands with global flags
- [x] REST API: 15 endpoints with Bearer auth
- [x] Mission Control web UI (3-panel)
- [x] Security: 25-pattern credential scrubbing, timing-safe auth, SHA-256 approval integrity

### Phase 2: Multi-Cloud Providers
- [x] AWS: 19 targets, production-grade multi-step adapters (ECS, ALB, Lambda)
- [x] AWS: Cross-cutting CloudWatch alarms, log retention
- [x] AWS: Partial failure recovery for multi-step operations
- [x] GCP: 17 targets (10 resource-specific, 7 project-scoped generic)
- [x] Azure: 18 targets (4 resource-specific, 2 identity-scoped, 12 ARM generic)
- [x] 4 rounds of QA hardening (security, engine robustness, parser correctness, API hardening)
- [x] CI pipeline (GitHub Actions)
- [x] State backup/restore system
- [x] 369 test functions, 16 packages all green

### Phase 3: Agent Interface Layer
- [x] MCP Server: 13 tools over stdio (validate, plan, apply, status, drift, approve, reject, rollback, list_runs, list_approvals, get_history, discover_beacons, connect_provider)
- [x] MCP tool annotations (ReadOnly, Destructive, Idempotent hints)
- [x] MCP error handling (isError responses, not protocol errors)
- [x] MCP security: path traversal protection (SafePath), three-surface scrubbing parity, ActiveProfile mutex
- [x] MCP partial failure: handleApply returns partial ApplyResult for orphaned resource recovery
- [x] Structured JSON output: `--format json` on all 11 CLI commands
- [x] Rich plan enrichment: risk scoring (1-10), rollback feasibility (safe/risky/impossible), cost-per-action, compliance mutations, PlanSummary aggregate
- [x] QA Round 5: 7 HIGH findings fixed (path traversal, scrub parity, race condition, nil guards, partial failure)
- [x] 473 test functions, 22 packages all green

---

## Phase 3: Agent Interface Layer (remaining)

**Goal:** Transform Beecon from "a CLI agents shell out to" into "a tool agents speak natively."

### 3.1 MCP Server (remaining)
- [ ] Streaming plan output for long-running operations
- [ ] Tool discovery and capability introspection (toolset grouping)
- [ ] Integration tests with MCP client (end-to-end stdio)

### 3.2 Structured Output (remaining)
- [ ] Machine-parseable errors with error codes and structured metadata (CLIError struct)
- [ ] Schema documentation for agent developers

### 3.3 Rich Plan Output (remaining)
- [ ] Cost delta (current monthly vs proposed monthly)
- [ ] Dependency chain depth metrics

---

## Phase 4: Agent Autonomy

**Goal:** Enable agents to handle more situations without falling back to a human.

### 4.1 Idempotent Apply
- [ ] Re-running apply on already-applied state is a safe no-op
- [ ] Partial failure recovery: resume from last successful action
- [ ] Extend phantom UPDATE skip pattern to full CREATE/DELETE idempotency

**Why:** Agents retry on failure. If an apply partially succeeds and the agent retries, it shouldn't duplicate resources.

### 4.2 `beecon diff`
- [ ] Compare beacon file vs current state without full plan cycle
- [ ] Show what changed in intent since last apply
- [ ] Structured output: added/removed/modified resources

**Why:** Lets agents reason cheaply — "did my edit actually change anything?" — before committing to a plan cycle.

### 4.3 Structured Error Recovery
- [ ] On failure, return structured recovery guidance
- [ ] Error taxonomy: auth, quota, conflict, transient, invalid-input
- [ ] Suggested remediation per error type
- [ ] "Retry-safe" flag on transient errors

**Why:** Instead of a stack trace, return `{"error": "...", "recovery": "run beecon refresh then retry"}`. Agents can follow recovery instructions autonomously.

### 4.4 Self-Healing Drift
- [ ] `beecon drift --reconcile` auto-generates a fix plan
- [ ] `beecon drift --reconcile --apply` auto-fixes in one step
- [ ] Reconciliation report showing what was corrected

**Why:** Agent detects drift, asks Beecon to fix it, done. No human loop.

---

## Phase 5: Multi-Cloud Parity

**Goal:** All three clouds work equally well — an agent shouldn't need to know which cloud it's targeting.

**Current gap (GCP vs AWS):** AWS has 20 resource types, 18 deep observation, 2 cross-cutting concerns, and full wiring layer (IAM/env vars/security groups). GCP has 12 resource-specific + 7 generic stubs, zero cross-cutting, zero wiring support. This means GCP beacons require 3-5x more explicit configuration than equivalent AWS beacons.

### 5.1 GCP Wiring Layer (Phase G1 — Highest Leverage)

Bring GCP into the wiring layer so agents get the same "smart defaults" as AWS.

- [ ] Add `gcpIAMActionsFor()` to wiring layer — map dependency pairs to GCP IAM roles (e.g., Cloud Run → Cloud SQL needs `roles/cloudsql.client`, Cloud Run → Secret Manager needs `roles/secretmanager.secretAccessor`)
- [ ] Add `gcpInferEnvVars()` — auto-inject Cloud SQL connection strings, Secret Manager secret names, Pub/Sub topic names from IR graph edges
- [ ] Add `gcpInferFirewallRules()` — generate firewall allow rules from dependency edges (e.g., Cloud Run → Cloud SQL implies allow TCP:5432)
- [ ] Cloud Monitoring alarms (post-apply `alarm_on`) — Cloud Run (request_count, latency), Cloud SQL (cpu, connections), Memorystore (memory), Compute Engine (cpu)
- [ ] Cloud Logging retention (post-apply `log_retention`) — set retention on Cloud Run and Cloud Functions log sinks

### 5.2 GCP Stub Promotion (Phase G2 — Fill the Holes)

Promote 7 generic stubs to resource-specific adapters with real SDK calls and deep observation.

- [ ] Cloud Functions — real CREATE/UPDATE/DELETE via Cloud Functions API (Lambda equivalent)
- [ ] GKE — cluster lifecycle via Container API (EKS equivalent)
- [ ] Cloud CDN — CDN configuration (CloudFront equivalent)
- [ ] Eventarc — event routing (EventBridge equivalent)
- [ ] API Gateway — HTTP API management (API Gateway v2 equivalent)
- [ ] Identity Platform — user pool management (Cognito equivalent)
- [ ] Cloud Monitoring — standalone alarm management (CloudWatch equivalent)

### 5.3 GCP Resilience (Phase G3 — Production Hardening)

- [ ] Multi-step partial results for Cloud Run (deploy → IAM → traffic split — return partial on mid-step failure)
- [ ] GCP-specific error classification — expand `isNotFound` with gRPC `codes.NotFound` and `googleapi.Error{Code: 404}` patterns
- [ ] Operation waiters — Cloud SQL and GKE creation takes minutes; add polling waiters
- [ ] withRetry for transient GCP errors (503, rate limits) with exponential backoff

### 5.4 GCP Observation Depth (Phase G4 — Drift Parity)

- [ ] Deepen Cloud Run observation — revision, scaling config, env vars (scrubbed), service URL, IAM policy
- [ ] Deepen Cloud SQL observation — database_version, tier, storage_auto_resize, backup_config, ip_addresses
- [ ] Deepen Memorystore observation — redis_version, memory_size_gb, host, port, auth_enabled
- [ ] Deepen remaining 9 resource types — match AWS-level field extraction
- [ ] Add observation for promoted stubs (depends on 5.2)

### 5.5 Azure Adapter Depth
- [ ] Resource-specific lifecycle adapters for top Azure targets (Container Apps, PostgreSQL Flexible, AKS)
- [ ] Replace ARM generic stubs with real CRUD operations
- [ ] Azure wiring layer (Managed Identity, env vars, NSG rules)
- [ ] Drift observation via Azure APIs

### 5.6 Provider Capability Matrix
- [ ] `beecon providers --format json` returns what's real vs simulated per cloud/target
- [ ] Agent can introspect: "can I actually deploy this, or will it be dry-run?"
- [ ] Transparency over silent simulation

**Why:** An agent doesn't pick a cloud — the user's domain block does. If GCP `cloud_sql` silently simulates while AWS `rds` actually provisions, the agent can't trust the tool.

**Recommended execution order:** G1 (wiring) → G2 (stubs) → G3 (resilience) → G4 (observation). G1 is the highest-leverage work — it transforms the GCP agent experience from "specify everything manually" to "declare intent, get smart defaults."

---

## Phase 6: Trust & Governance (Agent Safety)

**Goal:** Give agents enough policy context to self-govern, escalating to humans only when appropriate.

### 6.1 Cost Guardrails with Agent Context
- [ ] Plan output includes cost delta as structured data
- [ ] Configurable auto-approve threshold (e.g., "approve if cost delta < $100/mo")
- [ ] Budget trend tracking (are we approaching the limit?)

### 6.2 Blast Radius Scoring
- [x] Per-action risk score based on operation × node type (shipped in Phase 3)
- [x] Aggregate plan risk score via PlanSummary.AggregateRisk (shipped in Phase 3)
- [ ] Dependency-weighted risk (factor in downstream impact)
- [ ] Agent uses this to decide: auto-approve low-risk, escalate high-risk to human

### 6.3 Approval Delegation
- [ ] Policy-based auto-approval (cost, risk, resource type constraints)
- [ ] Agent can approve within policy bounds, escalate outside them
- [ ] Current human approval gates remain for out-of-bounds changes

### 6.4 Audit Trail with Agent Identity
- [ ] `--agent-id` flag on apply/approve for attribution
- [ ] Audit events track which agent, model, and context made each change
- [ ] Queryable agent activity history

**Why:** Current approval gates assume a human approver. For agents, you want: "approve automatically if cost delta < $100 and no deletes." When multiple agents can run apply, you need to know which one made each change.

---

## Phase 7: Ecosystem

**Goal:** Scale beyond single-agent, single-machine usage.

### 7.1 Beecon Modules / Templates
- [ ] Reusable `.beecon` fragments for common patterns (web app, API + DB, static site)
- [ ] Module registry (local directory or remote)
- [ ] Agent says "deploy a standard web app" and uses a module instead of generating from scratch

### 7.2 Remote State Backend
- [ ] S3 + DynamoDB lock backend
- [ ] GCS + object generation backend
- [ ] Azure Blob + lease backend
- [ ] Optimistic concurrency (version/etag on state object)
- [ ] Same `StateTransaction` API — engine code unchanged

**Why:** Only matters when multiple agents or CI runners share state. The current `Store` abstraction already supports swapping backends cleanly.

### 7.3 Plugin SDK
- [ ] Custom provider adapters
- [ ] Custom compliance frameworks
- [ ] Custom cost estimators
- [ ] Extension points for enterprise internal platforms

---

## Design Principles (Roadmap Guardrails)

1. **Agent-first, human-reviewable.** Every feature should be optimized for agent consumption. Human readability is a secondary output, not the primary design target.
2. **Structured over pretty.** JSON schemas over formatted tables. Error codes over error messages. Typed responses over freeform text.
3. **Safe by default, explicit to mutate.** Dry-run is the default. Mutation requires opt-in. Agents should never accidentally destroy infrastructure.
4. **Rich context for decisions.** Plan output should contain everything an agent needs to decide: cost, risk, compliance, rollback feasibility. The agent shouldn't need to make additional calls to gather decision context.
5. **Transparent capability boundaries.** If something is simulated, say so. If an operation can't be undone, say so. Agents need ground truth to reason correctly.
