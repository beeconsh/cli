# Beecon

The infrastructure language your agent uses to deploy to the cloud.

Beecon is a CLI and language for declaring infrastructure intent in `.beecon` files, then validating, planning, and applying that intent with audit visibility. It gives agent-driven workflows a deterministic interface for infrastructure operations while keeping policy boundaries, approvals, and state tracking in one place.

## Table of Contents
- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Typical Workflow](#typical-workflow)
- [Key Concepts](#key-concepts)
- [The .beecon Language](#the-beecon-language)
- [Commands Reference](#commands-reference)
- [Configuration](#configuration)
- [Profiles](#profiles)
- [Cross-Resource Wiring](#cross-resource-wiring)
- [Compliance](#compliance)
- [Cost Governance](#cost-governance)
- [Testing](#testing)
- [Provider Setup](#provider-setup)
- [Mission Control UI](#mission-control-ui)
- [Security](#security)
- [Troubleshooting](#troubleshooting)
- [Development](#development)
- [License](#license)

## Requirements
- Go 1.25+
- A valid `.beecon` file (default path: `infra.beecon`)
- Cloud credentials only if you use provider connectivity or live execution

## Quick Start
```bash
# build
go build -o beecon ./cmd/beecon

# create a starter beacon
beecon init

# validate and plan
beecon validate infra.beecon
beecon plan infra.beecon

# apply and inspect
beecon apply infra.beecon
beecon status
```

## Typical Workflow
1. Create or update `infra.beecon`
2. Run `beecon validate`
3. Run `beecon plan` and review actions, cost estimates, and compliance mutations
4. Run `beecon apply`
5. If gated, run `beecon approve <request-id>`
6. Check `beecon status`, `beecon history <resource-id>`, and `beecon drift`
7. Use `beecon rollback <run-id>` if needed
8. Use `beecon watch --interval 5m` for continuous drift monitoring

## Key Concepts
- **Intent**: Desired infrastructure state defined in `.beecon` files
- **Plan**: Ordered list of actions derived from intent vs current state
- **Apply**: Execution of plan actions, with optional interactive approval
- **Approval gate**: Boundary-controlled actions requiring explicit approval before execution
- **State store**: Local persisted state at `.beecon/state.json` (transactional, versioned)
- **Drift**: Divergence between declared intent and current state
- **Wiring**: Automatic inference of IAM policies, environment variables, and security group rules from `needs` declarations
- **Profiles**: Environment-specific configuration variants (production, staging, etc.)
- **Compliance**: Framework enforcement (HIPAA, SOC2) with automatic mutation of intent fields
- **Cost governance**: Budget enforcement and cheaper alternative suggestions

## The .beecon Language

```beecon
domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa, soc2]

  boundary {
    approve = [new_store]
    forbid  = [delete_store]
  }

  budget = 5000/mo
}

profile production {
  disk = 500gb
  replicas = 3
}

store postgres {
  engine = postgres
  disk   = 100gb
  apply  = [production]
}

service api {
  runtime = container(from: ./Dockerfile)
  expose  = public(port: 443)

  performance {
    latency = p95 < 200ms
    uptime  = 99.9%
  }

  needs {
    postgres = read_write
  }

  env {
    APP_ENV = production
  }
}

network vpc {
  cidr = 10.0.0.0/16
}
```

### Top-level blocks
- `domain` — Cloud provider, ownership, boundary policy, compliance, budget
- `service` — Application workloads (containers, functions)
- `store` — Data stores (databases, caches, object storage)
- `network` — Network resources (VPCs, subnets, security groups)
- `compute` — Compute resources (VMs, clusters)
- `profile` — Reusable configuration variants

### Nested blocks
- `boundary` — Approval gates (`approve`) and forbidden operations (`forbid`)
- `performance` — SLO definitions (latency, uptime thresholds)
- `needs` — Dependency declarations with access mode (read, write, read_write, admin)
- `env` — Environment variable injection

## Commands Reference

### Core lifecycle
```
beecon init [dir]                Initialize a new beecon project
beecon validate [path]           Validate beacon file syntax and semantics
beecon plan [path]               Generate execution plan with cost/compliance report
beecon apply [path]              Execute plan (--yes to auto-approve, --force to bypass budget)
beecon status                    Show infrastructure status (--filter DRIFTED,MATCHED,OBSERVED)
```

### Drift and monitoring
```
beecon drift [path]              Detect configuration drift
beecon refresh [path]            Update live state snapshots without changing status
beecon watch [path]              Continuous drift monitoring (--interval 5m)
```

### Approval workflow
```
beecon approve <request-id>      Approve pending actions
beecon reject <request-id>       Reject pending actions with reason
```

### History and rollback
```
beecon history <resource-id>     Show event timeline for a resource
beecon rollback <run-id>         Undo a previous run
```

### Discovery and import
```
beecon beacons                   List all discovered .beecon files
beecon import <provider> <type> <id> [region]   Import existing cloud resource
beecon connect <provider> [region]               Register cloud provider credentials
```

### Testing and observability
```
beecon test <test-file> [path]   Run .beecon-test assertions against plan
beecon performance <id> ...      Ingest performance breach event
beecon serve [addr]              Start Mission Control UI + REST API (default :8080)
```

### Global flags
```
--profile <name>    Active profile (CLI flag > BEECON_PROFILE env > .beecon/config.yaml)
--format text|json  Output format (default: text)
--debug             Enable debug logging to stderr
```

## Configuration
- `BEECON_EXECUTE=1` — Enable live cloud mutation calls (default: dry-run simulation)
- `BEECON_API_KEY=<key>` — Protect the HTTP API with Bearer token auth
- `BEECON_PROFILE=<name>` — Set active profile via environment
- `.beecon/config.yaml` — Project-level config (profile, etc.)
- `.beecon/state.json` — Local state store (auto-managed, do not edit)

## Profiles

Profiles provide environment-specific configuration variants:

```beecon
profile production {
  disk = 500gb
  replicas = 3
  instance_type = db.r6g.xlarge
}

profile staging {
  disk = 50gb
  replicas = 1
  instance_type = db.t3.medium
}

store postgres {
  engine = postgres
  apply = [production]
}
```

Select the active profile via `--profile`, `BEECON_PROFILE`, or `.beecon/config.yaml`.

## Cross-Resource Wiring

`needs` declarations automatically infer infrastructure wiring:

```beecon
service api {
  needs {
    postgres = read_write
    redis    = read
  }
}
```

This generates:
- **IAM policies** — Least-privilege actions based on target type and access mode
- **Environment variables** — Connection strings injected into the service (e.g., `POSTGRES_URL`, `REDIS_HOST`)
- **Security group rules** — Ingress rules scoped by dependency graph edges

Wiring metadata is stored in state and visible in plan output.

## Compliance

Declare compliance frameworks on your domain:

```beecon
domain acme {
  compliance = [hipaa, soc2]
}
```

The engine enforces the strictest constraints across declared frameworks:

| Framework | Enforced defaults |
|-----------|-------------------|
| HIPAA | `encryption_key=cmk`, `backup_enabled=true`, `log_retention_days=365`, `mfa_enabled=true` |
| SOC2 | `encryption_enabled=true`, `backup_enabled=true`, `audit_logging=true` |

Mutations are applied automatically and shown in plan output. Override with `compliance_override` on individual resources.

## Cost Governance

Declare a budget on your domain:

```beecon
domain acme {
  budget = 5000/mo
}
```

`beecon plan` estimates per-resource costs, flags budget exceedance, and suggests cheaper alternatives. Use `--force` on apply to bypass budget enforcement.

## Testing

Create `.beecon-test` files to assert plan behavior:

```
assert api intent.engine == "ecs"
assert postgres intent.storage_gib == "100"
assert_count CREATE 2
assert_count DELETE 0
```

Run with `beecon test assertions.beecon-test infra.beecon`.

## Provider Setup

### AWS
- Uses AWS SDK v2 credentials (environment, profile, IAM role)
- Validates identity via STS `GetCallerIdentity` during `beecon connect aws`
- Resource-specific live adapters: RDS, Aurora, S3, SQS, SNS, IAM, Secrets Manager, VPC/Subnet/Security Group
- Production-grade multi-step adapters: ECS (cluster + task def + Fargate service), ALB (LB + TG + listener), Lambda (VPC placement, layers, env vars)
- Cross-cutting: CloudWatch alarms (`alarm_on`), log retention (`log_retention`)
- Additional recognized targets run in dry-run simulation

### GCP
- Validates credentials via Google Cloud client initialization
- Resource-specific adapters: GCS, Cloud SQL, Cloud Run, Memorystore Redis, Pub/Sub, Secret Manager, VPC/Subnet/Firewall, IAM, Compute Engine, Cloud DNS
- Project-scoped generic adapters: Cloud Functions, API Gateway, Cloud CDN, Cloud Monitoring, GKE, Eventarc, Identity Platform

### Azure
- Validates via Azure Identity SDK credential initialization
- Resource-specific adapters: Blob Storage, Key Vault Secret, VNet/Subnet/NSG, Managed Identity
- Identity-scoped adapters: RBAC role assignment, Entra ID
- ARM generic adapters: Container Apps, PostgreSQL Flexible, MySQL Flexible, Azure Cache Redis, Functions, API Management, Service Bus, Event Grid, Front Door, CDN, DNS, Monitor, AKS, VM

## Mission Control UI

`beecon serve` starts a web UI at `http://localhost:8080` with three panels:

- **Intent Feed** — Recent runs and pending approvals with approve/reject actions
- **Resolution Graph** — Resource nodes, dependency edges, and planned actions
- **Audit Rail** — Chronological audit event stream

The UI polls for updates every 5 seconds. When `BEECON_API_KEY` is set, the UI prompts for an API key stored in `sessionStorage`.

## Security
- **Credential scrubbing**: All API responses, plan output, graph views, and state endpoints are scrubbed of sensitive values. The canonical registry of 25 sensitive key patterns lives in `internal/security/redact.go`
- **API authentication**: `BEECON_API_KEY` enables Bearer token auth with timing-safe comparison (`crypto/subtle`)
- **Approval integrity**: Apply captures a SHA-256 hash of the beacon file. Approve verifies the file hasn't been modified since — if it has, the approval is rejected
- **Transactional state**: All mutating operations use `LoadForUpdate`/`Commit`/`Rollback` to prevent TOCTOU races
- **State directory permissions**: `.beecon/` is created with mode `0700` (owner-only access)
- **State version guard**: Rejects state files from newer versions to prevent data corruption on downgrade
- **Provider retry**: Exponential backoff with jitter for throttling and transient cloud API errors
- **Drift error sanitization**: API strips ARNs and account IDs from error messages before returning to clients
- Keep cloud credentials out of source control
- Add `.beecon/` to `.gitignore`
- Use least-privilege IAM/service principals
- Review plans before apply
- Use approval gates for sensitive operations

## Troubleshooting
- **Validation fails**: Check block structure and required fields
- **Provider connect fails**: Verify credential environment/profile setup
- **Live apply fails**: Confirm `BEECON_EXECUTE=1` and required provider fields
- **Drift output empty**: Verify resource is managed and has provider identity in state
- **State version error**: You're running an older beecon against state from a newer version — upgrade beecon
- **Budget exceeded**: Review cost estimates in plan output; use `--force` to bypass

## Development
```bash
go test ./...           # run tests (245 test functions)
go test -race ./...     # with race detection
go vet ./...            # static analysis
```

Internal architecture docs: `docs/INTERNALS.md`

## License
MIT
