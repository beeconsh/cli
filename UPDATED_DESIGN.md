# Beecon Updated Design Doc (Implementation-Aligned)

## 1. Purpose

This document describes the current Beecon implementation in this repository and how it maps to the original Beecon design intent. It is the operational design reference for contributors.

## 2. Current Scope

The implementation is a local-runtime-first Beecon system with:

- `.beecon` language parser and semantic validation
- AST -> provider-agnostic Intent Graph (IR)
- Resolver planning from `intent vs state`
- Local persistent state/audit/approvals in `.beecon/state.json`
- Execution engine for `apply`, `approve`, `rollback`, `drift`
- Witness/performance breach ingestion
- Multi-cloud connection checks (AWS/GCP/Azure credentials)
- AWS adapter support matrix across Tier 1/2/3 resources with live execution for core targets
- Mission Control UI served from the same runtime
- CLI and HTTP API surfaces

## 3. Architecture

### 3.1 Layer Mapping

- Language layer:
  - `internal/parser`
  - `internal/ast`
- IR layer:
  - `internal/ir`
- Resolver layer:
  - `internal/resolver`
- Runtime/state/audit layer:
  - `internal/state`
  - `internal/engine`
- Witness layer:
  - `internal/witness`
- Integration/API layer:
  - `internal/provider`
  - `internal/discovery`
  - `internal/api`
- CLI entrypoint:
  - `cmd/beecon`

### 3.2 Control Flow

`beecon apply` flow:

1. Parse and validate `.beecon`
2. Build IR graph (nodes + edges + domain boundary)
3. Load state store (`.beecon/state.json`)
4. Build resolver plan (`CREATE/UPDATE/DELETE`) from diff
5. Annotate actions with boundary policy tags (`approve/forbid`)
6. Execute non-gated actions
7. Persist run/action/resource updates and audit events
8. If gated actions remain, create approval request and pause run

`beecon approve <request-id>`:

1. Load approval request
2. Validate request status and expiry
3. Execute pending action IDs
4. Mark approval and run as applied
5. Persist audit

## 4. Language Design (Current)

### 4.1 Supported Top-Level Blocks

- `domain`
- `service`
- `store`
- `network`
- `compute`
- `profile` (used by IR inheritance via `apply = [...]` on node blocks)

### 4.2 Supported Nested Blocks

- `boundary` (domain)
- `performance` (service/compute)
- `needs` (service/compute)
- `env` (service/compute)

### 4.3 Semantic Validation Rules

- Exactly one `domain` block is required
- `domain` requires `cloud` and `owner`
- Duplicate node names across service/store/network/compute are rejected
- `needs` and `performance` are restricted to service/compute/profile
- profile inheritance references must resolve to declared `profile` blocks
- `needs` references must target known node names

## 5. Intent Graph (IR)

### 5.1 Node Types

- `SERVICE`
- `STORE`
- `NETWORK`
- `COMPUTE`

### 5.2 IR Components

- `DomainNode`: cloud/owner/compliance/boundary
- `IntentNode`: type, intent map, performance map, env map, needs list
- `Profile`: reusable field/child-block defaults merged into intent nodes via `apply`
- `Edge`: dependency relation (`From -> To`)

### 5.3 Dependency Behavior

- `needs` creates directed edges from dependency node to consumer node
- Planner performs topological sort and node-type precedence ordering

## 6. Resolver and Execution

### 6.1 Plan Operations

- `CREATE`: intent exists, no managed state record
- `UPDATE`: intent hash changed or record marked drifted
- `DELETE`: managed resource removed from current intent
- `FORBIDDEN`: blocked by boundary policy at execution time

### 6.2 Boundary Gate Handling

Boundary tags currently mapped from action patterns:

- `new_store`
- `delete_store`
- `instance_type_change`
- `expose_public`

Behavior:

- if tag in `forbid`: action becomes `FORBIDDEN`
- if tag in `approve`: action is deferred into approval request

### 6.3 Rollback

Rollback creates a new run and applies inverse behavior in reverse executed order:

- inverse(`CREATE`) -> `DELETE`
- inverse(`UPDATE`) -> `NOOP`
- inverse(`DELETE`) -> `RESTORE`

## 7. State Store Design

Path: `.beecon/state.json`

Persisted structures:

- `resources`: resource records (intent snapshot, live state, status, history)
- `runs`: run metadata and executed actions
- `actions`: action registry for replay/approve/rollback
- `approvals`: pending/approved requests with expiry
- `audit`: immutable-style timeline events
- `connections`: provider connection metadata
- `performance_events`: witness-ingested breaches

Resource statuses:

- `MATCHED`
- `DRIFTED`
- `UNPROVISIONED`
- `OBSERVED`

## 8. CLI Surface

Implemented commands:

- `init`
- `validate`
- `plan`
- `apply`
- `status`
- `beacons`
- `drift`
- `approve`
- `history`
- `rollback`
- `connect`
- `performance`
- `serve`

## 9. HTTP API Surface

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

## 10. Provider Integration

### 10.1 AWS

- Uses AWS SDK v2
- Validates identity via STS `GetCallerIdentity` during `connect aws`
- Support matrix (recognized targets):
  - Tier 1: ECS, RDS, Aurora Serverless, ElastiCache, S3, ALB, VPC/Subnets/Security Groups, IAM, Secrets Manager
  - Tier 2: Lambda, API Gateway, SQS, SNS, CloudFront, Route53, CloudWatch
  - Tier 3: EKS, EventBridge, Cognito, EC2
- Live execution implemented for core targets:
  - RDS (instance + Aurora cluster create path)
  - S3
  - SQS
  - SNS
  - IAM (role)
  - Secrets Manager
  - EC2 VPC/Subnet/Security Group primitives
- Recognized-but-not-live-complete targets are dry-run simulated by default; when `BEECON_EXECUTE=1`, they return explicit adapter completion errors.

### 10.2 GCP

- Validates credential presence and client initialization via Google Cloud Storage SDK

### 10.3 Azure

- Validates env credentials and identity initialization via Azure Identity SDK

## 11. Testing

Current unit/integration tests cover:

- parser syntax + semantic validation
- resolver dependency ordering
- profile inheritance behavior
- engine apply/approval/rollback and forbid policy behavior
- state store save/load/hash behavior
- discovery scanner behavior
- witness candidate generation
- provider target detection/support matrix behavior
- API validate/performance endpoints

## 12. Known Gaps vs Original Target Vision

- Parser is implemented in Go (not Rust + pest)
- Full live mutation coverage for every AWS target in the support matrix is not complete yet
- Approval timeout handling is enforced on approve, but no background expiry processor
- Drift now performs live provider observation for implemented AWS targets; fallback remains local for unsupported targets

## 13. Next Implementation Milestones

1. Complete live execution adapters for all AWS matrix targets (ALB/ECS/Lambda/API Gateway/CloudFront/Route53/CloudWatch/EKS/EventBridge/Cognito/EC2 full paths)
2. Add explicit reject flow and background expiry processing for approvals
3. Extend live reconciliation breadth and granular drift diffing by resource type
4. Add API authn/authz and multi-tenant boundaries
5. Optional Rust parser core swap behind stable AST/IR boundary
