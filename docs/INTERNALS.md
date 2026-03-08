# Beecon

End-to-end Beecon runtime implementation in `beecon/` with `.beecon` DSL parsing, intent graph planning, state/audit persistence, approve gates, rollback, performance breach ingestion, multi-cloud connect checks, and an HTTP API.

## Implemented

- Language layer
  - `.beecon` block parser (`domain`, `service`, `store`, `network`, `compute`, `profile`)
  - semantic validation (single root domain, allowed nesting, dependency references)
  - escape-aware comment stripping (`\"` inside quoted strings)
  - quote-aware list splitting (commas inside quoted list items are preserved)
- IR layer
  - provider-agnostic intent graph (`IntentNode`, dependencies, domain boundary)
- Resolver layer
  - plan generation from intent vs state diff (`CREATE`, `UPDATE`, `DELETE`)
  - topological ordering with type precedence (`network -> store -> compute -> service`)
  - boundary evaluation (`auto`, `approve`, `forbid`)
- State + audit layer
  - persistent store in `.beecon/state.json`
  - resource state records, run records, action records, approval requests, audit events
- Execution layer
  - `apply` with partial execution + approval pause
  - `approve <id>` to resume gated actions
  - `rollback <run-id>` reverse execution of completed run actions
- Witness layer (runtime telemetry)
  - performance breach ingestion and candidate response generation
- Discovery
  - repo scan for `.beecon` files
- Multi-cloud connection checks
  - AWS: live identity check via AWS SDK v2 STS `GetCallerIdentity`
  - GCP: Google Cloud Storage client init check (`GOOGLE_APPLICATION_CREDENTIALS`)
  - Azure: Azure Identity credential init check (`AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_CLIENT_SECRET`)
- AWS provider execution adapters
  - Tier 1/2/3 target registry is wired (ECS, RDS, Aurora Serverless, ElastiCache, S3, ALB, VPC/Subnet/SG, IAM/Secrets Manager, Lambda, API Gateway, SQS/SNS, CloudFront, Route53, CloudWatch, EKS, EventBridge, Cognito, EC2)
  - Production-grade multi-step lifecycle adapters:
    - ECS: cluster → task definition → Fargate service (CREATE/UPDATE/DELETE with partial-result recovery)
    - ALB: load balancer → target group → listener with cascade delete + state evolution fallback
    - Lambda: VPC placement, layers, env vars
    - RDS: enhanced monitoring, CloudWatch log exports, read replica intent, Aurora Serverless
    - ElastiCache: AZ mode, auth token rotation, snapshot retention
  - Cross-cutting post-apply concerns:
    - CloudWatch alarms (`alarm_on` intent) with resource-scoped Dimensions per target type
    - CloudWatch Logs retention (`log_retention` intent) for Lambda/ECS log groups
  - Input validation: ECS cpu/memory Fargate ranges, ALB port ranges, RDS monitoring intervals + role_arn requirement, ElastiCache AZ mode, alarm_on syntax
  - Extracted helpers: `buildECSTaskDef` (DRY for CREATE/UPDATE), `lambdaVpcConfig`, `alarmDimensionsForTarget`
  - Partial `ApplyResult` returned on multi-step failure (orphaned resource recovery)
  - Additional live SDK adapters:
    - S3, SQS, SNS, IAM (role), Secrets Manager, EC2 VPC/Subnet/Security Group primitives
  - Remaining recognized targets run via dry-run simulation or explicit live-mode validation errors
- GCP provider execution adapters
  - Target matrix wired across Tier 1/2/3
  - Resource-specific live adapters implemented for:
    - GCS, Cloud SQL, Cloud Run, Memorystore Redis, Pub/Sub, Secret Manager
    - VPC/Subnet/Firewall, IAM (service accounts), Compute Engine, Cloud DNS
  - Remaining recognized GCP targets use project-scoped generic adapters with live project verification:
    - Cloud Functions, API Gateway, Cloud CDN, Cloud Monitoring, GKE, Eventarc, Identity Platform
- Azure provider execution adapters
  - Target matrix wired across Tier 1/2/3
  - Resource-specific live adapters implemented for:
    - Blob Storage, Key Vault Secret, VNet/Subnet/NSG, Managed Identity
  - Additional live adapters:
    - RBAC role assignment adapter
    - Entra ID identity-scoped adapter
  - ARM generic-resource adapters implemented for:
    - Container Apps, PostgreSQL Flexible, MySQL Flexible, Azure Cache Redis
    - Functions, API Management, Service Bus, Event Grid
    - Front Door, CDN, DNS, Monitor, AKS, VM
- Security layer (`internal/security/`)
  - Canonical sensitive key registry (`IsSensitiveKey`) — single source of truth for 16 sensitive key patterns (password, secret_value, token, api_key, private_key, connection_string, database_url, dsn, etc.)
  - `ScrubMap(map[string]interface{})` — scrubs values of sensitive keys, returns new map (nil-safe)
  - `ScrubStringMap(map[string]string)` — for intent node scrubbing
  - `ScrubChanges(map[string]string)` — for plan action diff scrubbing
  - Used by engine, API server, resolver, and executor — no duplicate key lists
- Transactional state API
  - `Store.LoadForUpdate()` → `*StateTransaction` — acquires mutex, returns state for mutation
  - `tx.Commit()` — saves state and releases mutex
  - `tx.Rollback()` — releases mutex without saving (safe to call multiple times)
  - All mutating engine methods (Apply, Approve, Reject, Drift, Rollback, Connect, IngestPerformanceBreach) use this pattern
  - Read-only methods (Status, Runs, Approvals, History, Audit) use plain `Load()`
  - `expireApprovalsInline(st)` — pure function that mutates state in-place within a transaction (no Load/Save)
  - `HashMap` normalizes `float64` → `int64` for whole numbers to prevent JSON round-trip phantom diffs
- Approval integrity
  - `ApprovalRequest.IntentHash` — SHA-256 of beacon file content at `Apply` time
  - `Approve()` re-reads the file and compares hashes; rejects if modified
- Audit cap
  - Maximum 10,000 audit events retained; oldest events are trimmed on write
- Context propagation
  - All public engine methods take `context.Context` as first parameter
  - API handlers pass `r.Context()`
  - CLI creates signal-cancellable context (`SIGINT`/`SIGTERM`)
- API surface
  - `GET/POST /api/beacons`
  - `POST /api/beacon/validate`
  - `POST /api/resolve` (plan actions scrubbed of credentials)
  - `GET /api/graph` (nodes and actions scrubbed of credentials)
  - `GET /api/state` (intent snapshots and live state scrubbed)
  - `GET /api/runs`
  - `GET /api/approvals`
  - `GET /api/audit`
  - `GET /api/history`
  - `POST /api/drift` (drift output scrubbed of credentials)
  - `POST /api/approve`
  - `POST /api/reject`
  - `POST /api/connect`
  - `GET/POST /api/performance`
  - Optional Bearer token auth via `BEECON_API_KEY` (timing-safe comparison)
- Mission Control UI
  - Served at `/` via `internal/ui/handler.go`
  - When `BEECON_API_KEY` is set, API key is injected via `<meta>` tag and JS adds `Authorization` header to all fetch calls

## CLI

```bash
beecon init [dir]
beecon validate [infra.beecon]
beecon plan [infra.beecon]
beecon apply [infra.beecon]
beecon status
beecon beacons
beecon drift [infra.beecon]
beecon approve <request-id> [approver]
beecon reject <request-id> [approver] [reason]
beecon history <resource-id>
beecon rollback <run-id>
beecon connect <aws|gcp|azure> [region]
beecon performance <resource-id> <metric> <observed> <threshold> [duration]
beecon serve [:8080]
```

## Quick Start

```bash
cd beecon
go test ./...
go run ./cmd/beecon validate testdata/sample.beecon
go run ./cmd/beecon plan testdata/sample.beecon
go run ./cmd/beecon apply testdata/sample.beecon
go run ./cmd/beecon status
go run ./cmd/beecon serve :8080
```

## End-to-End Workflow

### 1) Initialize a New Beacon

```bash
cd beecon
go run ./cmd/beecon init
```

This creates `infra.beecon` in the current directory.

### 2) Validate Syntax + Semantics

```bash
go run ./cmd/beecon validate infra.beecon
```

Expected: `valid infra.beecon`

### 3) Generate the Resolver Plan

```bash
go run ./cmd/beecon plan infra.beecon
```

Expected output includes:
- `domain: ...`
- `nodes: ... edges: ...`
- ordered actions (`CREATE`/`UPDATE`/`DELETE`)
- approval markers for gated actions (`[approval:<tag>]`)

### 4) Apply the Plan

```bash
go run ./cmd/beecon apply infra.beecon
```

If no approval is required:
- run completes with `executed: N`

If approval is required:
- output includes `approval_required: <request-id>`
- run state becomes `PENDING_APPROVAL` until approved

### 5) Approve Gated Actions (When Needed)

```bash
go run ./cmd/beecon approve <request-id> [approver]
```

Expected:
- gated actions execute
- run transitions to `APPLIED`

### 6) Inspect Current State + Audit Trail

```bash
go run ./cmd/beecon status
go run ./cmd/beecon history <resource-id>
```

Examples:
- `service.api`
- `store.postgres`

### 7) Roll Back a Run

```bash
go run ./cmd/beecon rollback <run-id>
```

Expected:
- a new rollback run id is returned
- previously executed actions are reversed in reverse order

## Notes

- By default execution is dry-run safe. Set `BEECON_EXECUTE=1` to enable live AWS SDK mutation calls for implemented adapters.
- Mission Control UI is served at `/` and consumes API data from `/api/*`. Set `BEECON_API_KEY` to require auth.
- Rollback now issues cloud calls (DELETE for rollback of CREATE, CREATE for rollback of DELETE) when `BEECON_EXECUTE=1`.
- AWS `isNotFound` detection uses smithy SDK error types (structured code matching) with string fallback.

## Live AWS Inputs (Current)

When `BEECON_EXECUTE=1`, resources require explicit intent fields. Validation runs before any cloud calls.

### Required Fields
- **RDS / Aurora**: `username`, `password`; `monitoring_role_arn` when `enhanced_monitoring=true`
- **ECS**: `image_uri`; validates `cpu` ∈ {256,512,1024,2048,4096}, `memory` ∈ 512-30720 MB
- **ALB**: `subnet_ids`; `vpc_id` when `target_port` is set; validates port ranges 1-65535
- **Lambda**: `role_arn`, `code_s3_bucket`, `code_s3_key`; validates memory 128-10240 MB, timeout 1-900s
- **Subnet**: `vpc_id` (and optional `cidr`)
- **Security Group**: `vpc_id`
- **ElastiCache**: validates `az_mode` ∈ {single-az, cross-az}

### Optional Cross-Cutting Intents
- `alarm_on`: creates a CloudWatch alarm scoped to the resource (e.g., `"cpu > 80"`). Supported targets: rds, ecs, lambda, ec2, elasticache, eks.
- `log_retention`: sets CloudWatch Logs retention (e.g., `"30d"`). Supported targets: lambda, ecs.

### Partial Failure Recovery
Multi-step operations (ECS cluster→task def→service, ALB LB→TG→listener) return partial `ApplyResult` on mid-sequence failure, allowing the engine to track orphaned resources for cleanup.

If required fields are missing, apply fails with explicit validation errors before creating partial infrastructure.
