# Configuration Roadmap

> The syntax is fine. The resolver/executor is thin.

Beecon's flat intent model (`key = value`) is the right abstraction. The gap isn't expressiveness — it's depth. The executor needs to read more keys, and the resolver needs to infer more from simple declarations.

This document maps what power users need to configure, organized by when they hit the wall.

---

## Phase 1: Unblocks Production Deployments [COMPLETE]

These are the configs where a power user says "I literally cannot deploy this without controlling X." An infra engineer evaluating beecon will try a real workload, hit one of these, and stop.

All are flat key-value. No syntax changes required.

### Data Safety

| Intent Key | Resource Types | What It Controls |
|---|---|---|
| `backup_retention = 7d` | RDS, Cloud SQL, Azure Postgres | Automated backup retention period |
| `backup_window = 03:00-04:00` | RDS | Preferred backup window |
| `kms_key = alias/my-key` | RDS, S3, Secrets Manager | Customer-managed encryption key |
| `multi_az = true` | RDS | Multi-AZ failover deployment |
| `deletion_protection = true` | RDS, Cloud SQL | Prevent accidental deletion |

### Network Isolation

| Intent Key | Resource Types | What It Controls |
|---|---|---|
| `subnet_ids = [subnet-a, subnet-b]` | RDS, ECS, Lambda, EKS | Subnet placement |
| `security_group_ids = [sg-xxx]` | RDS, ECS, Lambda, EC2 | Security group attachment |
| `publicly_accessible = false` | RDS (hardcoded today) | Public endpoint exposure |
| `ingress = tcp:443:10.0.0.0/16` | Security Group | Inbound rules (compact format) |
| `egress = tcp:443:0.0.0.0/0` | Security Group | Outbound rules |

### Identity & Permissions

| Intent Key | Resource Types | What It Controls |
|---|---|---|
| `role_arn = arn:aws:iam::...` | ECS, Lambda | Execution role |
| `managed_policies = [arn:...]` | IAM Role | Attached managed policies |
| `trust_services = [ecs-tasks, lambda]` | IAM Role | Trust policy principals (hardcoded to ecs-tasks today) |

### Current State

- RDS: reads `engine`, `instance_type`, `disk`, `username`, `password`. Hardcodes `PubliclyAccessible: false`, `StorageEncrypted: true`. No backup, KMS, multi-AZ, subnet group, parameter group.
- Security Groups: reads `vpc_id` only. No rules.
- IAM: hardcoded trust for `ecs-tasks.amazonaws.com`. No policies.
- S3: no config at all — bucket name auto-generated, no versioning, encryption, or policies.

---

## Phase 2: Makes It Production-Grade [COMPLETE]

Once someone can deploy, they need operational control. These are Week 1 problems.

### Compute Tuning

| Intent Key | Resource Types | What It Controls |
|---|---|---|
| `memory = 512` | Lambda, Cloud Run, ECS | Memory allocation (MB) |
| `timeout = 30s` | Lambda, Cloud Run | Execution timeout |
| `cpu = 256` | ECS, Cloud Run | CPU units |
| `health_check_path = /health` | ECS, ALB | Health check endpoint |
| `desired_count = 2` | ECS | Task count |
| `env.DATABASE_URL = ...` | Lambda, ECS, Cloud Run | Environment variables (via `env {}` block, already parsed) |

### Database Tuning

| Intent Key | Resource Types | What It Controls |
|---|---|---|
| `parameter_group = my-pg-params` | RDS, ElastiCache | Custom parameter group |
| `subnet_group = my-db-subnets` | RDS, ElastiCache | DB subnet group |
| `read_replicas = 1` | RDS, Cloud SQL | Read replica count |
| `storage_type = gp3` | RDS | Storage type (gp2, gp3, io1) |
| `iops = 3000` | RDS | Provisioned IOPS |
| `max_connections = 200` | RDS (via parameter group) | Connection limit |

### Observability

| Intent Key | Resource Types | What It Controls |
|---|---|---|
| `alarm_on = cpu > 80%` | Any compute/store | Auto-create CloudWatch alarm |
| `log_retention = 30d` | Lambda, ECS | CloudWatch log retention |
| `enhanced_monitoring = true` | RDS | Enhanced monitoring |

### Load Balancer

| Intent Key | Resource Types | What It Controls |
|---|---|---|
| `certificate_arn = arn:aws:acm:...` | ALB | TLS certificate |
| `health_check_path = /health` | ALB target group | Health check |
| `health_check_interval = 30s` | ALB target group | Check interval |
| `routing_rules = ...` | ALB | Path/host-based routing |

### Current State

- Lambda: reads `role_arn`, `code_s3_bucket`, `code_s3_key`, `runtime`, `handler`, `memory`, `timeout`, `env.*`, `subnet_ids`, `security_group_ids`, `layer_arns`. Supports VPC placement and Lambda layers.
- ECS: full Fargate support — `image_uri` (required), `cpu`, `memory`, `desired_count`, `container_port`, `role_arn`, `subnet_ids`, `security_group_ids`, `env.*`. Creates cluster + task definition + service.
- ALB: full stack — `subnet_ids`, `scheme`, `vpc_id`, `target_port`, `listener_port`, `certificate_arn`, `health_check_path`, `health_check_interval`, `health_check_timeout`, `healthy_threshold`, `unhealthy_threshold`, `target_type`, `security_group_ids`. Auto-creates target group + listener inline. Certificate auto-upgrades to HTTPS.
- ElastiCache: reads `engine`, `node_type`, `num_cache_nodes`, `parameter_group`, `subnet_group`, `security_group_ids`, `auth_token`, `snapshot_retention`, `az_mode`. Multi-AZ resilience via `az_mode = cross-az`.
- RDS: reads `enhanced_monitoring`, `monitoring_interval`, `monitoring_role_arn`, `log_exports`, `read_replica_count` (intent stored, creation deferred to Phase 3).
- Cross-cutting: `log_retention` (e.g. "30d") sets CloudWatch Logs retention on Lambda/ECS log groups. `alarm_on` (e.g. "cpu > 80") creates CloudWatch alarms as side-effects with per-target metric inference.

---

## Phase 3: The Moat

These are the features that, if beecon handles them well, become the competitive advantage. Month 1 problems that create long-term lock-in.

### Cross-Resource Wiring (Resolver Intelligence)

Today `needs { postgres = read_write }` creates a dependency edge in the graph. Phase 3 means the resolver acts on it:

```beecon
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
    uploads  = write
    events   = publish
  }
}
```

The resolver should infer and create:
- IAM policy granting `api` read/write to the `postgres` RDS instance
- IAM policy granting `api` write to the `uploads` S3 bucket
- IAM policy granting `api` publish to the `events` SNS topic
- Environment variables: `DATABASE_URL`, `UPLOADS_BUCKET`, `EVENTS_TOPIC_ARN`
- Security group rules allowing `api` to reach `postgres` on port 5432

No user configuration needed. The resolver knows what "read_write to a postgres store" means.

### Compliance-as-Config (Resolver Intelligence)

```beecon
domain acme {
  compliance = [hipaa, soc2]
}
```

The resolver should enforce:
- All data stores: encryption at rest with CMK, audit logging, backup retention >= 7 days
- All networks: no public subnets for data-tier resources, VPC flow logs enabled
- All services: no public endpoints for internal services, TLS everywhere
- All secrets: rotation enabled, no plaintext in environment variables

This is worth more than any individual config key because it applies globally and removes the burden of remembering 30 security settings across 10 resources.

### Environment Promotion (Profile Intelligence)

```beecon
profile staging {
  instance_type = db.t3.micro
  multi_az = false
  desired_count = 1
  backup_retention = 1d
}

profile production {
  instance_type = db.r6g.xlarge
  multi_az = true
  desired_count = 3
  backup_retention = 30d
  deletion_protection = true
}
```

Profiles exist today but aren't deeply wired. The multiplier is: a platform engineer writes profiles once, and every developer/agent inherits production-grade defaults.

### Cost Governance (Boundary Intelligence)

```beecon
boundary {
  budget = 5000/mo
}
```

Exists in the scaffold template but isn't enforced. If the resolver could:
1. Estimate monthly cost from instance types, storage, and data transfer
2. Refuse to plan actions that would exceed the budget boundary
3. Suggest cheaper alternatives ("db.r6g.xlarge costs ~$400/mo, db.r6g.large at ~$200/mo meets your performance SLO")

That's a governance feature Terraform doesn't have.

---

## Phase 4: Production Hardening

Phases 1-3 built breadth — 19 AWS targets, 16 CLI commands, compliance, cost governance, cross-resource wiring. Phase 4 builds depth: test what exists, harden what ships, polish what users touch.

The investment thesis shifts: **every untested code path is a production incident waiting to happen.**

### Current State (Post Phase 3)

| Dimension | Status | Key Metric |
|---|---|---|
| **AWS executor** | Production-ready | 19/19 targets with real SDK calls |
| **GCP executor** | Partial | 13/20 targets (7 generic stubs) |
| **Azure executor** | Early | 9/24 targets (15 generic stubs) |
| **Needs wiring** | 2/3 executed | IAM + env vars execute; SG rules infer-only |
| **State & drift** | Functional | Hash-based drift works; no schema migration, no drift history |
| **CLI DX** | Good surface | 16 commands; no JSON output, no diff visualization |
| **Test coverage** | Uneven | Security 100%, provider 13%, engine 50%, CLI 0% |
| **CI pipeline** | Missing | No automated test/lint on PR |

### P0: Foundation (Before Everything Else)

These are prerequisites. Nothing else ships safely without them.

| Item | Effort | Why |
|---|---|---|
| CI pipeline (test + lint on every PR) | ~1 day | Tests don't matter if they don't run |
| Provider integration tests (mock SDK clients, test CREATE/UPDATE/DELETE per target) | ~2 weeks | 13% coverage on the core cloud layer is a ticking bomb |
| Engine orchestration tests (Plan, Drift, multi-action failure recovery) | ~1 week | 50% coverage on the orchestrator that coordinates everything |
| Complete SG wiring execution (InferredSGRules → intent["ingress"]) | ~1 day | Phase 3 promised this; inference exists, execution is ~50 lines away |

### P1: CLI & Developer Experience

Makes beecon usable by real teams in CI/CD pipelines and daily workflows.

| Item | Effort | Why |
|---|---|---|
| `--format json` on plan/apply/status/drift | ~3 days | CI/CD integration requires machine-readable output |
| Visual diff in plan output (`+`/`-`/`~` markers) | ~2 days | Users need to see what's changing before approving |
| `--verbose`/`--debug` flag | ~1 day | Can't debug failed applies without operation logs |
| Status filtering (`--filter DRIFTED\|PENDING_APPROVAL`) | ~1 day | 50+ resources = wall of text |
| Interactive approval flow (show details, prompt Y/N) | ~2 days | Current apply→manual-approve is disconnected |

### P2: State Hardening

Production safety for teams running beecon against real infrastructure.

| Item | Effort | Why |
|---|---|---|
| State schema migration system | ~3 days | Version field exists but is unused; any schema change breaks state |
| `beecon refresh` command (fetch live state without planning) | ~2 days | Users need "just sync state" without generating actions |
| Drift history (track when drift was first detected, trend analysis) | ~2 days | Pattern recognition for recurring drift |
| Retry/backoff on cloud API calls | ~3 days | Rate limits will cause failures at scale |
| Approval timeout/expiry enforcement | ~1 day | Pending approvals can hang indefinitely |

### P3: Multi-Cloud Parity

Expands addressable market beyond AWS-only teams.

| Item | Effort | Why |
|---|---|---|
| GCP remaining 7 adapters (Cloud Functions, API Gateway, CDN, Monitoring, GKE, Eventarc, Identity Platform) | ~3-4 weeks | 65% → 100% coverage |
| Azure remaining 15 adapters (Container Apps, Postgres, MySQL, Redis, Functions, API Mgmt, Service Bus, Event Grid, Front Door, CDN, DNS, Monitor, AKS, VMs) | ~6-8 weeks | 38% → 100% coverage |
| GCP/Azure Observe depth (real Describe/Get calls for drift detection) | ~2 weeks | Drift detection needs real adapters, not cached state |

### P4: Differentiation

Moat-deepening features that no competitor offers.

| Item | Effort | Why |
|---|---|---|
| Resource import (`beecon import <provider-id>`) | ~2 weeks | Brownfield adoption — bring existing infra under management |
| Scheduled drift checks (cron-style or watch mode) | ~1 week | Proactive drift detection without manual `beecon drift` |
| Mission Control UI (apply + approve in browser) | ~2 weeks | Currently read-only monitoring; needs to be a control plane |
| `beecon test` framework (validate beacon correctness beyond syntax) | ~1 week | Policy-as-code testing before apply |

---

## Implementation Priority (All Phases)

| Priority | Area | Effort | Impact |
|---|---|---|---|
| **P0** | Phase 1 data safety keys (backup, KMS, multi-AZ) | ~2 days per provider | Unblocks production use |
| **P0** | Phase 1 network isolation (security group rules, subnet placement) | ~2 days | Unblocks production use |
| **P0** | Phase 4 CI pipeline + provider/engine tests | ~3 weeks | Unblocks safe shipping |
| **P0** | Phase 4 complete SG wiring execution | ~1 day | Completes Phase 3 promise |
| **P1** | Phase 2 compute tuning (memory, timeout, env vars) | ~3 days | Makes serverless/containers usable |
| **P1** | Phase 2 database tuning (parameter groups, subnet groups) | ~2 days | Production database config |
| **P1** | Phase 4 CLI DX (`--format json`, diff, verbose, filtering) | ~1.5 weeks | Makes beecon CI/CD-ready |
| **P2** | Phase 1 IAM/permissions (policies, trust) | ~3 days | Required for real workloads |
| **P2** | Phase 2 ALB (target groups, TLS, health checks) | ~3 days | Required for web services |
| **P2** | Phase 4 state hardening (migration, refresh, retry) | ~2 weeks | Production state safety |
| **P3** | Phase 3 cross-resource wiring | ~2 weeks | The moat — resolver intelligence |
| **P3** | Phase 3 compliance-as-config | ~2 weeks | The moat — resolver intelligence |
| **P3** | Phase 4 multi-cloud parity (GCP + Azure) | ~10-12 weeks | Full multi-cloud story |
| **P4** | Phase 3 cost governance | ~1 week | Differentiation |
| **P4** | Phase 4 import, scheduled drift, Mission Control | ~5 weeks | Differentiation |

---

## Design Principle

> The moment you add `security_group { ingress { ... } }` to the syntax, you've rebuilt Terraform with different braces. The whole value prop collapses.

Power users don't need more syntax. They need:
1. **More intent keys** the executor reads (Phase 1-2)
2. **Smarter resolver inference** from simple declarations (Phase 3)
3. **Profiles** to encode organizational standards once (exists today, needs depth)
4. **Boundaries** to enforce governance globally (exists today, needs depth)
5. **Tested, hardened execution** they can trust with real infrastructure (Phase 4)

The flat model scales. The resolver is where the intelligence lives. The tests are where the trust lives.
