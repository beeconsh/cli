# Beecon

End-to-end Beecon runtime implementation in `beecon/` with `.beecon` DSL parsing, intent graph planning, state/audit persistence, approve gates, rollback, performance breach ingestion, multi-cloud connect checks, and an HTTP API.

## Implemented

- Language layer
  - `.beecon` block parser (`domain`, `service`, `store`, `network`, `compute`, `profile`)
  - semantic validation (single root domain, allowed nesting, dependency references)
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
  - Live SDK execution implemented now for high-frequency resources:
    - RDS (Postgres/MySQL + Aurora cluster create path)
    - S3
    - SQS
    - SNS
    - IAM (role)
    - Secrets Manager
    - EC2 VPC/Subnet/Security Group primitives
  - Remaining recognized targets run via dry-run simulation or explicit live-mode validation errors
- API surface
  - `GET/POST /api/beacons`
  - `POST /api/beacon/validate`
  - `POST /api/resolve`
  - `GET /api/graph`
  - `GET /api/state`
  - `GET /api/runs`
  - `GET /api/approvals`
  - `GET /api/audit`
  - `GET /api/history`
  - `POST /api/drift`
  - `POST /api/approve`
  - `POST /api/connect`
  - `GET/POST /api/performance`

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
- Mission Control UI is served at `/` and consumes API data from `/api/*`.

## Live AWS Inputs (Current)

When `BEECON_EXECUTE=1`, some resources require explicit intent fields:

- `RDS` / `Aurora`: `username`, `password` (credentials are now required; no default fallback)
- `ALB`: `subnet_ids` (comma-separated or list form)
- `Lambda`: `role_arn`, `code_s3_bucket`, `code_s3_key`
- `Subnet`: `vpc_id` (and optional `cidr`)
- `Security Group`: `vpc_id`

If required fields are missing, apply fails with explicit validation errors before creating partial infrastructure.
