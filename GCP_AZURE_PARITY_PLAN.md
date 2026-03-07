# GCP + Azure Parity Plan (Execution Backlog)

This is the concrete plan to bring GCP/Azure to AWS-level runtime parity in Beecon.

## Delivery Strategy

- Order: `GCP Tier 1 -> Azure Tier 1 -> GCP Tier 2 -> Azure Tier 2 -> GCP Tier 3 -> Azure Tier 3`
- Definition of parity per target:
  - live `Apply` implemented
  - live `Observe` implemented (for drift)
  - required intent validation enforced
  - state provider IDs persisted
  - tests added (unit + integration dry-run)

## Current Baseline

- AWS: partial live adapters + drift observe in place
- GCP/Azure: full matrix now wired with live apply/observe adapter coverage
  - Mix of resource-specific adapters and generic adapters
  - Remaining parity work is depth parity (provider-native semantics), not wiring parity

## Architecture Tasks (Shared)

1. Provider abstraction refinement
- File: `internal/provider/executor.go`
- Add explicit provider-specific target interfaces:
  - `applyGCP(...)`, `observeGCP(...)`
  - `applyAzure(...)`, `observeAzure(...)`
- Keep existing `ApplyRequest`/`ApplyResult` types; add optional `Meta map[string]string` for provider-specific operational metadata.

2. Target detection split by provider
- File: `internal/provider/executor.go`
- Add:
  - `detectGCPTarget(req ApplyRequest) string`
  - `detectAzureTarget(req ApplyRequest) string`
- Keep detection deterministic and testable.

3. Required field validators
- New file: `internal/provider/validate.go`
- Add provider-target validators:
  - `validateGCPTargetInput(target string, intent map[string]interface{}) error`
  - `validateAzureTargetInput(target string, intent map[string]interface{}) error`
- Fail before SDK calls.

4. Support matrices
- File: `internal/provider/executor.go`
- Add:
  - `GCPSupportMatrix map[string]string`
  - `AzureSupportMatrix map[string]string`
- Tier tags: `tier1|tier2|tier3`

## GCP Backlog

### Tier 1 (Ship First)

Targets:
- `cloud_run` (service)
- `cloud_sql` (postgres/mysql)
- `memorystore_redis`
- `gcs`
- `vpc` / `subnet` / `firewall`
- `iam`
- `secret_manager`

Implementation tasks:

1. SDK wiring
- Files:
  - `internal/provider/gcp_apply.go`
  - `internal/provider/gcp_observe.go`
- Dependencies:
  - Cloud Run Admin client
  - Cloud SQL Admin client
  - Memorystore client
  - Cloud Storage client
  - Compute Network/Subnetwork/Firewall clients
  - IAM client
  - Secret Manager client

2. Target handlers
- `applyGCPCloudRun`, `observeGCPCloudRun`
- `applyGCPCloudSQL`, `observeGCPCloudSQL`
- `applyGCPMemorystore`, `observeGCPMemorystore`
- `applyGCPGCS`, `observeGCPGCS`
- `applyGCPVPC`, `applyGCPSubnet`, `applyGCPFirewall`, matching observe handlers
- `applyGCPIAM`, `observeGCPIAM`
- `applyGCPSecretManager`, `observeGCPSecretManager`

3. Required intent fields (initial)
- Cloud Run: `image`
- Cloud SQL: `engine`, `tier`, `region`, `username`, `password`
- Memorystore: `tier`, `memory_size_gb`, `region`
- GCS: `location`
- Subnet/firewall: `network` + CIDR/rules

Acceptance criteria:
- `BEECON_EXECUTE=1` can create/update/delete each target
- drift uses live provider data and flags missing resources
- provider IDs stored in `state.ResourceRecord.ProviderID`

### Tier 2

Targets:
- `cloud_functions`
- `api_gateway`
- `pubsub` (topic/subscription)
- `cloud_cdn`
- `cloud_dns`
- `cloud_monitoring`

Tasks:
- add apply/observe handlers + validators per target

### Tier 3

Targets:
- `gke`
- `eventarc`
- `identity_platform`
- `compute_engine`

Tasks:
- add apply/observe + validators
- enforce strong defaults + explicit required fields for high-risk resources

## Azure Backlog

### Tier 1 (Ship First)

Targets:
- `container_apps` (or AKS-light equivalent path for service)
- `postgres_flexible` / `mysql_flexible`
- `azure_cache_redis`
- `blob_storage`
- `vnet` / `subnet` / `nsg`
- `rbac` / `managed_identity`
- `key_vault_secret`

Implementation tasks:

1. SDK wiring
- Files:
  - `internal/provider/azure_apply.go`
  - `internal/provider/azure_observe.go`
- Dependencies:
  - ARM resources/network/storage/containers/DB/redis/keyvault clients

2. Target handlers
- `applyAzureContainerApps`, `observeAzureContainerApps`
- DB handlers for flexible servers
- Redis handlers
- Blob storage handlers
- Network handlers
- Identity/RBAC handlers
- Key Vault secret handlers

3. Required intent fields (initial)
- Container app: `image`, `resource_group`, `location`, `environment_id`
- DB: `sku`, `version`, `admin_username`, `admin_password`
- Blob: `resource_group`, `location`, `account_tier`
- VNet/Subnet/NSG: address prefixes + group/location

Acceptance criteria:
- same as GCP Tier 1 parity criteria

### Tier 2

Targets:
- `functions`
- `api_management`
- `service_bus` / `event_grid`
- `front_door` / `cdn`
- `dns`
- `monitor`

### Tier 3

Targets:
- `aks`
- `event_grid_advanced`
- `entra_id`
- `vm`

## Engine/State Integration Tasks

1. Drift enrichment
- File: `internal/engine/engine.go`
- Ensure `Observe` output normalized and persisted in `LiveState`.
- Add drift reason metadata for provider mismatch categories.

2. Audit enhancements
- File: `internal/engine/engine.go`
- Add provider operation metadata to audit event `Data` (target, provider id, region).

3. Error normalization
- New file: `internal/provider/errors.go`
- Normalize not-found and retriable errors across AWS/GCP/Azure.

## Tests (Required for each target)

1. Unit tests
- File pattern:
  - `internal/provider/*_test.go`
- Must include:
  - target detection coverage
  - required intent field validation
  - support matrix completeness for planned tier

2. Engine tests
- File: `internal/engine/*_test.go`
- Must include:
  - apply success path for each target in dry-run mode
  - delete/rollback behavior
  - drift marking from observe missing state

3. API tests
- File: `internal/api/server_test.go`
- Must include endpoint coverage for drift/connect/resolve for new providers.

4. Optional live-cloud smoke tests
- New directory: `internal/provider/smoke/`
- Gated by env vars and skipped in default CI.

## CI/Quality Gates

For each PR slice:
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- No new TODO stubs in shipped target handlers

## Suggested PR Slices

1. `gcp-tier1-cloudsql-gcs`
2. `gcp-tier1-network-secrets-iam`
3. `azure-tier1-db-blob`
4. `azure-tier1-network-keyvault-identity`
5. `gcp-tier2`
6. `azure-tier2`
7. `gcp-tier3`
8. `azure-tier3`

## Immediate Next Action

Start with `gcp-tier1-cloudsql-gcs` in one slice:
- implement handlers + validators + detection + tests
- keep all non-implemented GCP targets returning explicit live-mode errors, dry-run simulated.
