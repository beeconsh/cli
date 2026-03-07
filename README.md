# Beecon 🐝

The infrastructure language your agent uses to deploy to the cloud.

Beecon is a CLI and language for declaring infrastructure intent in `.beecon` files, then validating, planning, and applying that intent with audit visibility. It exists to give agent-driven workflows a deterministic interface for infrastructure operations while keeping policy boundaries, approvals, and state tracking in one place.

With Beecon, you can:
- Define infrastructure intent in a readable `.beecon` file format
- Generate execution plans before applying changes
- Apply changes with approval gates for sensitive operations
- Track state, run history, and audit events locally
- Use one command surface across AWS, GCP, and Azure setup paths

## Table of Contents
- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Typical Workflow](#typical-workflow)
- [Key Concepts](#key-concepts)
- [How It Works](#how-it-works)
- [The .beecon Language](#the-beecon-language)
- [Commands Reference](#commands-reference)
- [Configuration](#configuration)
- [Provider Setup](#provider-setup)
- [Security](#security)
- [Troubleshooting](#troubleshooting)
- [Development](#development)
- [Getting Help](#getting-help)
- [License](#license)

## Requirements
- Go 1.25+
- A valid `.beecon` file (default path: `infra.beecon`)
- Cloud credentials only if you use provider connectivity or live execution

## Quick Start
```bash
# from repo root
cd beecon

# verify build/test
go test ./...

# create a starter beacon
go run ./cmd/beecon init

# validate and plan
go run ./cmd/beecon validate infra.beecon
go run ./cmd/beecon plan infra.beecon

# apply and inspect
go run ./cmd/beecon apply infra.beecon
go run ./cmd/beecon status
```

## Typical Workflow
1. Create or update `infra.beecon`
2. Run `validate`
3. Run `plan` and review actions
4. Run `apply`
5. If gated, run `approve <request-id>`
6. Check `status`, `history`, and `drift`
7. Use `rollback <run-id>` if needed

## Key Concepts
- Intent: Desired infrastructure state defined in `.beecon`
- Plan: Ordered list of actions derived from intent vs current state
- Apply: Execution of plan actions
- Approval Gate: Boundary-controlled actions requiring explicit approval
- State Store: Local persisted state at `.beecon/state.json`

## How It Works
- Beecon parses `.beecon` intent files
- It validates syntax and semantic references
- It builds a plan (`CREATE`, `UPDATE`, `DELETE`)
- It applies actions and records run/audit state
- It supports drift checks against current recorded and provider-observed state

## The .beecon Language
Example:

```beecon
domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)

  boundary {
    approve = [new_store]
    forbid  = [delete_store]
  }
}

store postgres {
  engine = postgres
  disk   = 100gb
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
}
```

## Commands Reference
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

## Configuration
- `BEECON_EXECUTE=1`: enable live AWS mutation calls for implemented adapters
- Default mode (unset): dry-run-safe simulation
- Local state path: `.beecon/state.json`

## Provider Setup
- AWS: supported with live execution for implemented adapters
- GCP: credential/connectivity checks plus live execution for selected Tier 1 adapters
- Azure: credential/connectivity checks currently available

AWS live adapters currently implemented:
- RDS
- S3
- SQS
- SNS
- IAM
- Secrets Manager
- EC2 primitives (VPC/Subnet/Security Group)

Other recognized AWS resource targets run in dry-run simulation mode by default.

GCP live adapters currently implemented:
- Cloud Storage (GCS)
- Cloud SQL

## Security
- Keep cloud credentials out of source control
- Add `.beecon/` to your project's `.gitignore` to avoid committing state and credentials.
- Use least-privilege IAM/service principals for provider credentials
- Review plans before apply
- Use approval gates for sensitive operations

## Troubleshooting
- Validation fails: check block structure and required fields in `.beecon`
- Provider connect fails: verify credential environment/profile setup
- Live apply fails: confirm `BEECON_EXECUTE=1` and required provider fields are present
- Drift output empty when expected: verify resource is managed and has provider identity in state

## Development
```bash
# run tests
go test ./...

# race detection
go test -race ./...

# vet
go vet ./...
```

Internal implementation notes are in `docs/INTERNALS.md`.

## Getting Help
- Open issues: https://github.com/beeconsh/cli/issues
- Repository: https://github.com/beeconsh/cli

## License
MIT
