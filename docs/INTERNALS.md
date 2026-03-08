# Beecon Internals

Implementation reference for contributors. Covers architecture, control flow, state design, and provider integration.

## Architecture

### Package Map

```
cmd/beecon/          CLI entrypoint (Cobra commands, flag registration)
internal/
  api/               REST API server (15 endpoints, Bearer auth middleware)
  ast/               Abstract syntax tree for .beecon files
  btest/             Test assertion framework (.beecon-test files)
  classify/          Node type classification (SERVICE/STORE/NETWORK/COMPUTE)
  cli/               Output formatting, color, JSON output, interactive prompts
  cloud/             Cloud provider abstraction types
  compliance/        Framework enforcement (HIPAA, SOC2) with mutation pipeline
  cost/              Budget parsing, pricing estimates, cheaper alternatives
  discovery/         Beacon file scanner (.beecon pattern matching)
  engine/            Core orchestrator — parse, plan, apply, drift, rollback, import
  ir/                Intermediate representation (intent graph, profiles, edges)
  logging/           Debug logging (stderr, silent by default)
  parser/            Beacon file parser (regex-based DSL)
  provider/          AWS/GCP/Azure executors, retry logic, target detection
  resolver/          Plan builder (topological sort, intent diff, boundary gates)
  scaffold/          Project initialization (init command)
  security/          Sensitive key registry, scrubbing functions
  state/             State store (file-locked JSON, transactions, schema migrations)
  ui/                Mission Control UI (embedded HTML/JS)
  wiring/            Cross-resource wiring (IAM, env vars, SG rules)
  witness/           Performance breach remediation suggestions
```

### Control Flow

**`beecon apply` flow:**

1. Parse and validate `.beecon` file
2. Build IR graph (nodes + edges + domain boundary)
3. Apply compliance mutations (HIPAA/SOC2 framework enforcement)
4. Wire cross-resource dependencies (IAM policies, env vars, SG rules)
5. Load state store (`.beecon/state.json`) via transaction
6. Build resolver plan (`CREATE`/`UPDATE`/`DELETE`) from intent diff
7. Annotate actions with boundary policy tags (`approve`/`forbid`)
8. Estimate costs and check budget
9. Execute non-gated actions via provider executor
10. Persist run/action/resource updates and audit events
11. If gated actions remain, create approval request and pause run
12. Return result with cost report and compliance mutations

**`beecon approve <request-id>`:**

1. Load state via transaction
2. Validate request status (must be PENDING) and expiry
3. Verify beacon file integrity (SHA-256 hash comparison)
4. Execute pending action IDs
5. Mark approval as APPROVED, run as APPLIED
6. Persist audit events

**`beecon plan` flow:**

1. Parse + validate + build IR
2. Apply compliance mutations and wiring
3. Diff intent against state → generate actions
4. Skip phantom UPDATEs (empty change sets on DRIFTED resources)
5. Estimate costs with budget check
6. Return plan with cost report, compliance mutations, wiring metadata

## Engine

The engine (`internal/engine/engine.go`) is the central orchestrator. Key methods:

| Method | Transaction | Description |
|--------|-------------|-------------|
| `Plan` | Read-only | Parse → IR → compliance → wiring → diff → cost |
| `Apply` | Write | Plan + execute + persist state (returns partial result on failure) |
| `Approve` | Write | Validate + execute gated actions + update approval |
| `Reject` | Write | Reject approval + clear ApprovalBlocked on resources |
| `Rollback` | Write | Reverse a run (CREATE→DELETE, DELETE→RESTORE) |
| `Drift` | Write | Compare intent hashes + observe live state |
| `Refresh` | Write | Update live state snapshots without changing status |
| `Import` | Write | Observe existing cloud resource → create state record |
| `Status` | Read-only | Return current state snapshot |

### State Mutation Pattern

All write operations follow the transactional pattern:

```go
tx, err := e.store.LoadForUpdate()  // acquire file lock + mutex
if err != nil {
    return nil, err
}
defer tx.Rollback()  // safe no-op if Commit was called

// ... modify tx.State ...

if err := tx.Commit(); err != nil {
    return nil, err
}
```

### copyMap

`copyMap` uses JSON round-trip for deep copy to prevent aliased nested maps from cross-contaminating state between resources.

### expireApprovalsInline

Pure function that mutates state in-place within a transaction. Expires approvals past their `ExpiresAt` timestamp. Returns `true` if any were expired.

## State Store

**Path:** `.beecon/state.json`
**Schema version:** 2 (migration framework in `internal/state/migration.go`)
**Directory permissions:** `0700` (owner-only)

### Schema

```go
State {
    Version           int
    Resources         map[string]*ResourceRecord
    Runs              map[string]*RunRecord
    Actions           map[string]*ActionRecord
    Approvals         map[string]*ApprovalRequest
    Audit             []*AuditEvent       // capped at 10,000
    Connections       map[string]*Connection
    PerformanceEvents []*PerformanceEvent
}
```

### ResourceRecord

```go
ResourceRecord {
    ResourceID, NodeType, NodeName, Provider, ProviderID string
    Managed bool
    IntentSnapshot map[string]interface{}
    IntentHash     string
    LiveState      map[string]interface{}
    Status         ResourceStatus  // MATCHED, DRIFTED, UNPROVISIONED, OBSERVED
    LastAppliedRun, LastOperation string
    ApprovalBlocked bool
    LastSeen        *time.Time
    Wiring          *WiringMetadata  // InferredEnvVars, InferredPolicy, InferredSGRules
    EstimatedCost   float64
    DriftFirstDetected *time.Time    // v2 migration
    DriftCount         int           // v2 migration
}
```

### Transaction API

```go
tx, _ := store.LoadForUpdate()  // file lock + mutex
tx.Commit()                      // save + release
tx.Rollback()                    // release without save (idempotent)
```

### Schema Migrations

Version-gated migrations run automatically on load when `state.Version < CurrentVersion`:
- v1 → v2: Adds `DriftFirstDetected` and `DriftCount` fields to `ResourceRecord`

State files from newer versions are rejected with an explicit error to prevent data corruption.

### HashMap

Normalizes `float64` → `int64` for whole numbers before hashing, preventing JSON round-trip phantom diffs.

## Parser

Regex-based DSL parser (`internal/parser/parser.go`):

- Top-level blocks: `domain`, `service`, `store`, `network`, `compute` (alphanumeric + underscore + hyphen names; no dots)
- Profile blocks: `profile <name>` (separate regex pattern)
- Nested blocks: `boundary`, `performance`, `needs`, `env`
- Escape-aware comment stripping (`\"` inside quoted strings)
- Quote-aware list splitting (commas inside quoted list items preserved)
- Semantic validation: single domain, no duplicate names, valid `needs` references, profile inheritance resolution

## IR Layer

Provider-agnostic intent graph:

- `DomainNode`: cloud, owner, compliance frameworks, boundary policy, budget
- `IntentNode`: type (SERVICE/STORE/NETWORK/COMPUTE), intent map, performance map, env map, needs list
- `Profile`: reusable defaults merged via `apply = [profile_name]`
- `Edge`: dependency relation (from → to)

Profile application: validates active profile matches at least one resource's `apply` list. Returns error on unknown profile (likely typo).

## Resolver

Plan builder (`internal/resolver/plan.go`):

- `CREATE`: intent exists, no managed state record
- `UPDATE`: intent hash changed or status is DRIFTED (skips phantom updates with empty change sets)
- `DELETE`: managed resource removed from current intent
- `FORBIDDEN`: blocked by boundary policy

Topological sort with type precedence: `network → store → compute → service`.

Boundary tags mapped from action patterns: `new_store`, `delete_store`, `instance_type_change`, `expose_public`.

## Wiring

Cross-resource wiring (`internal/wiring/wiring.go`):

1. **IAM policy inference**: For each `needs` edge, look up target type + access mode → generate least-privilege actions
2. **Environment variable inference**: Auto-inject connection strings from target resources
3. **Security group rule inference**: Generate ingress rules scoped by graph edges (not global)

Mode validation: rejects invalid combinations (e.g., `admin` on `NETWORK`). Warns about wildcard access.

Wiring metadata stored in `ResourceRecord.Wiring` for audit trail.

## Classification

`internal/classify/classify.go` provides canonical node classification:

- `ClassifyNode(nodeType, nodeName, intent) → AWSTarget`
- Used by both the provider executor (`detectAWSTarget`) and the wiring layer
- Single source of truth — no duplicated classification logic

## Compliance

Framework enforcement pipeline (`internal/compliance/`):

1. Collect constraints from declared frameworks (HIPAA, SOC2)
2. Validate `compliance_override` fields
3. Resolve strictest defaults across frameworks (e.g., HIPAA `cmk` beats SOC2 `true` for encryption)
4. Apply mutations in-place to intent nodes
5. Validate all nodes against rules
6. Report: violations, overrides, mutations, warnings

## Cost

Budget and cost governance (`internal/cost/`):

- Parse budget strings (`5000/mo`, `60000/yr`) → normalize to monthly
- Estimate per-resource costs by type and instance class
- Flag budget exceedance with warnings
- Suggest cheaper alternatives with monthly savings delta
- Budget of `$0/mo` is rejected as invalid

## Provider Executor

### Architecture

The executor (`internal/provider/executor.go`) dispatches to provider-specific handlers:

```
ApplyRequest → detectTarget → validate → execute (or simulate) → ApplyResult
```

Dry-run mode (default) returns simulated results. Live mode (`BEECON_EXECUTE=1`) calls real cloud APIs.

### Retry

Generic retry with exponential backoff + jitter (`internal/provider/retry.go`):

```go
withRetry[T](ctx, name, maxAttempts, fn) (T, error)
```

- Base delay: 500ms, max delay: 10s, default max attempts: 3
- Retries: throttling exceptions, 5xx, transient errors
- Does not retry: permission denied, not found, validation errors

### AWS Adapters

**Resource-specific** (full CREATE/UPDATE/DELETE lifecycle):
- RDS (instance + Aurora cluster), ECS (cluster → task def → service), ALB (LB → TG → listener)
- Lambda (VPC placement, layers, env vars), ElastiCache (AZ mode, auth token, snapshot)
- S3, SQS, SNS, IAM (role), Secrets Manager, VPC/Subnet/Security Group

**Cross-cutting concerns:**
- `alarm_on`: CloudWatch alarms with resource-scoped Dimensions per target type
- `log_retention`: CloudWatch Logs retention for Lambda/ECS log groups

**Partial failure recovery:** Multi-step operations return partial `ApplyResult` on mid-sequence failure for orphaned resource tracking.

### GCP Adapters

**Resource-specific:** GCS, Cloud SQL, Cloud Run, Memorystore Redis, Pub/Sub, Secret Manager, VPC/Subnet/Firewall, IAM, Compute Engine, Cloud DNS

**Project-scoped generic:** Cloud Functions, API Gateway, Cloud CDN, Cloud Monitoring, GKE, Eventarc, Identity Platform

### Azure Adapters

**Resource-specific:** Blob Storage, Key Vault Secret, VNet/Subnet/NSG, Managed Identity

**Identity-scoped:** RBAC role assignment, Entra ID

**ARM generic:** Container Apps, PostgreSQL Flexible, MySQL Flexible, Azure Cache Redis, Functions, API Management, Service Bus, Event Grid, Front Door, CDN, DNS, Monitor, AKS, VM

## Security

### Sensitive Key Registry

Canonical list of 25 sensitive key patterns (`internal/security/redact.go`):

```
password, secret_value, token, admin_password, secret, secret_key, access_key,
api_key, private_key, connection_string, client_secret, master_password,
auth_token, database_url, connection_url, dsn, ssh_key, credentials,
refresh_token, passphrase, encryption_key, signing_key, tls_key, certificate, bearer
```

Matching is case-insensitive on the key's base name (after last `.`).

### Scrubbing Functions

| Function | Input type | Used by |
|----------|-----------|---------|
| `ScrubMap` | `map[string]interface{}` | API responses, live state |
| `ScrubStringMap` | `map[string]string` | Intent fields, env maps |
| `ScrubChanges` | `map[string]string` | Plan action diffs |

All scrubbing replaces values with `**REDACTED**`.

### API Authentication

- Middleware: timing-safe Bearer token comparison (`crypto/subtle.ConstantTimeCompare`)
- Source: `BEECON_API_KEY` environment variable
- Auth failures logged with client IP for security monitoring
- UI handles auth client-side via `sessionStorage` prompt (no server-side key injection)

### Drift Error Sanitization

API strips ARNs and AWS account IDs from drift error messages before returning to clients, preventing infrastructure metadata leakage.

## API Surface

15 endpoints served at `/api/`:

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/beacons` | GET, POST | Yes | Discover / register beacon files |
| `/api/beacon/validate` | POST | Yes | Validate beacon syntax |
| `/api/resolve` | POST | Yes | Generate plan (scrubbed) |
| `/api/graph` | GET | Yes | Resource graph with nodes, edges, actions |
| `/api/state` | GET | Yes | Full state snapshot (scrubbed) |
| `/api/runs` | GET | Yes | Run history |
| `/api/approvals` | GET | Yes | Pending approvals |
| `/api/audit` | GET | Yes | Audit events (filterable by resource) |
| `/api/history` | GET | Yes | Resource event history |
| `/api/drift` | POST | Yes | Detect drift (errors sanitized) |
| `/api/apply` | POST | Yes | Execute plan (supports `force` flag) |
| `/api/approve` | POST | Yes | Approve pending actions |
| `/api/reject` | POST | Yes | Reject pending actions |
| `/api/connect` | POST | Yes | Register cloud provider |
| `/api/performance` | GET, POST | Yes | Performance breach events |

Auth column: required when `BEECON_API_KEY` is set.

## Mission Control UI

Embedded single-page app served at `/` by `internal/ui/handler.go`:

- Three-panel layout: Intent Feed, Resolution Graph, Audit Rail
- Polls API every 5 seconds (pauses when tab is hidden, exponential backoff on errors)
- Actions: Apply, Approve, Reject with confirmation dialogs
- Auth: client-side `sessionStorage` prompt when API requires Bearer token
- XSS protection: all dynamic content escaped via `esc()` helper

## Test Framework

`.beecon-test` assertion format (`internal/btest/`):

```
assert <node-name> <field> <op> <value>
assert_count <operation> <count>
```

Operations: `==`, `!=`, `contains`. Evaluated against `PlanResult`.

## Testing

245 test functions across the codebase covering:

- Parser syntax and semantic validation
- Resolver dependency ordering and boundary gates
- Profile inheritance and unknown profile detection
- Engine apply/approval/rollback/reject/import/drift
- State store save/load/hash/transaction/migration/permissions
- Provider target detection and support matrix
- Wiring IAM inference, env var injection, SG rule scoping
- Compliance framework enforcement
- Cost budget parsing and validation
- API endpoint behavior
- UI handler output
- Security key registry

## Known Gaps

- Parser is Go (not Rust + pest per original design)
- GCP/Azure generic adapters lack resource-specific lifecycle depth
- No background approval expiry processor (expiry enforced inline on access)
- Drift observation depth varies by provider and target type
