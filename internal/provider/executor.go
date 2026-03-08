package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigatewayv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	eventbridgetypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	smithy "github.com/aws/smithy-go"
	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

// ApplyRequest describes a resolver action to execute with a cloud provider.
type ApplyRequest struct {
	Provider string
	Region   string
	Action   *state.PlanAction
	Intent   map[string]interface{}
	Record   *state.ResourceRecord
}

// ApplyResult captures provider-side execution output.
type ApplyResult struct {
	ProviderID string
	LiveState  map[string]interface{}
}

// ObserveResult captures the current provider state for a managed resource.
type ObserveResult struct {
	Exists     bool
	ProviderID string
	LiveState  map[string]interface{}
}

// Executor performs provider-specific apply and observe operations.
type Executor interface {
	Apply(ctx context.Context, req ApplyRequest) (*ApplyResult, error)
	Observe(ctx context.Context, provider, region string, rec *state.ResourceRecord) (*ObserveResult, error)
	IsDryRun() bool
}

type DefaultExecutor struct {
	dryRun bool
}

// AWSSupportMatrix lists all supported AWS targets by tier.
var AWSSupportMatrix = map[string]string{
	"ecs":                   "tier1",
	"rds":                   "tier1",
	"rds_aurora_serverless": "tier1",
	"elasticache":           "tier1",
	"s3":                    "tier1",
	"alb":                   "tier1",
	"vpc":                   "tier1",
	"subnet":                "tier1",
	"security_group":        "tier1",
	"iam":                   "tier1",
	"secrets_manager":       "tier1",
	"lambda":                "tier2",
	"api_gateway":           "tier2",
	"sqs":                   "tier2",
	"sns":                   "tier2",
	"cloudfront":            "tier2",
	"route53":               "tier2",
	"cloudwatch":            "tier2",
	"eks":                   "tier3",
	"eventbridge":           "tier3",
	"cognito":               "tier3",
	"ec2":                   "tier3",
}

func NewExecutor() *DefaultExecutor {
	return &DefaultExecutor{dryRun: os.Getenv("BEECON_EXECUTE") != "1"}
}

func (e *DefaultExecutor) IsDryRun() bool {
	return e.dryRun
}

func (e *DefaultExecutor) Apply(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	provider := strings.ToLower(req.Provider)
	if provider == "" {
		provider = "local"
	}
	switch provider {
	case "aws":
		return e.applyAWS(ctx, req)
	case "gcp":
		return e.applyGCP(ctx, req)
	case "azure":
		return e.applyAzure(ctx, req)
	default:
		return simulatedApply(req, "generic"), nil
	}
}

func (e *DefaultExecutor) Observe(ctx context.Context, provider, region string, rec *state.ResourceRecord) (*ObserveResult, error) {
	switch strings.ToLower(provider) {
	case "aws":
		return e.observeAWS(ctx, region, rec)
	case "gcp":
		return e.observeGCP(ctx, region, rec)
	case "azure":
		return e.observeAzure(ctx, region, rec)
	default:
		if rec == nil {
			return &ObserveResult{Exists: false, LiveState: map[string]interface{}{}}, nil
		}
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
}

func (e *DefaultExecutor) applyAWS(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	target := detectAWSTarget(req)
	if err := validateAWSInput(target, req.Intent); err != nil {
		return nil, fmt.Errorf("aws input validation (%s): %w", target, err)
	}
	if e.dryRun {
		return simulatedApply(req, target), nil
	}
	if req.Region == "" {
		req.Region = "us-east-1"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(req.Region))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}

	switch target {
	case "rds":
		return e.applyAWSRDS(ctx, rds.NewFromConfig(cfg), req)
	case "rds_aurora_serverless":
		return e.applyAWSRDS(ctx, rds.NewFromConfig(cfg), req)
	case "alb":
		return e.applyAWSALB(ctx, elbv2.NewFromConfig(cfg), req)
	case "ecs":
		return e.applyAWSECS(ctx, ecs.NewFromConfig(cfg), req)
	case "s3":
		return e.applyAWSS3(ctx, s3.NewFromConfig(cfg), req)
	case "lambda":
		return e.applyAWSLambda(ctx, lambda.NewFromConfig(cfg), req)
	case "api_gateway":
		return e.applyAWSAPIGateway(ctx, apigatewayv2.NewFromConfig(cfg), req)
	case "sqs":
		return e.applyAWSSQS(ctx, sqs.NewFromConfig(cfg), req)
	case "sns":
		return e.applyAWSSNS(ctx, sns.NewFromConfig(cfg), req)
	case "secrets_manager":
		return e.applyAWSSecrets(ctx, secretsmanager.NewFromConfig(cfg), req)
	case "iam":
		return e.applyAWSIAM(ctx, iam.NewFromConfig(cfg), req)
	case "elasticache":
		return e.applyAWSElastiCache(ctx, elasticache.NewFromConfig(cfg), req)
	case "cloudfront":
		return e.applyAWSCloudFront(ctx, cloudfront.NewFromConfig(cfg), req)
	case "route53":
		return e.applyAWSRoute53(ctx, route53.NewFromConfig(cfg), req)
	case "cloudwatch":
		return e.applyAWSCloudWatch(ctx, cloudwatch.NewFromConfig(cfg), req)
	case "eks":
		return e.applyAWSEKS(ctx, eks.NewFromConfig(cfg), req)
	case "eventbridge":
		return e.applyAWSEventBridge(ctx, eventbridge.NewFromConfig(cfg), req)
	case "cognito":
		return e.applyAWSCognito(ctx, cognitoidentityprovider.NewFromConfig(cfg), req)
	case "vpc", "subnet", "security_group", "ec2":
		return e.applyAWSEC2(ctx, ec2.NewFromConfig(cfg), req, target)
	default:
		return nil, fmt.Errorf("aws target %q is recognized but requires additional adapter implementation for live execution (set BEECON_EXECUTE!=1 for dry-run)", target)
	}
}

func (e *DefaultExecutor) applyAWSRDS(ctx context.Context, c *rds.Client, req ApplyRequest) (*ApplyResult, error) {
	id := req.RecordProviderID()
	if id == "" {
		id = identifierFor(req.Action.NodeName)
	}
	switch req.Action.Operation {
	case "CREATE":
		engine := defaultString(intent(req.Intent, "engine", "type"), "postgres")
		user, pass, err := rdsCredentials(req.Intent)
		if err != nil {
			return nil, err
		}
		if strings.Contains(engine, "aurora") {
			_, err := c.CreateDBCluster(ctx, &rds.CreateDBClusterInput{
				DBClusterIdentifier: awsString(id),
				Engine:              awsString("aurora-postgresql"),
				MasterUsername:      awsString(user),
				MasterUserPassword:  awsString(pass),
				EngineMode:          awsString("serverless"),
			})
			if err != nil {
				return nil, fmt.Errorf("rds create aurora serverless cluster: %w", err)
			}
			return &ApplyResult{ProviderID: id, LiveState: map[string]interface{}{"service": "rds", "engine": "aurora-postgresql", "cluster_id": id}}, nil
		}
		allocated := parseStorageGiB(intent(req.Intent, "disk"))
		if allocated == 0 {
			allocated = 20
		}
		class := defaultString(intent(req.Intent, "instance_type"), "db.t3.micro")
		storageType := defaultString(intent(req.Intent, "storage_type"), "gp3")
		createIn := &rds.CreateDBInstanceInput{
			DBInstanceIdentifier: awsString(id),
			Engine:               awsString(engine),
			DBInstanceClass:      awsString(class),
			MasterUsername:       awsString(user),
			MasterUserPassword:   awsString(pass),
			AllocatedStorage:     awsInt32(allocated),
			PubliclyAccessible:   awsBool(false),
			StorageEncrypted:     awsBool(true),
			StorageType:          awsString(storageType),
			MultiAZ:             awsBool(parseBoolIntent(req.Intent, "multi_az", false)),
			BackupRetentionPeriod: awsInt32(parseIntIntent(req.Intent, "backup_retention", 7)),
			DeletionProtection:   awsBool(parseBoolIntent(req.Intent, "deletion_protection", false)),
		}
		if v := intent(req.Intent, "backup_window"); v != "" {
			createIn.PreferredBackupWindow = awsString(v)
		}
		if v := intent(req.Intent, "kms_key"); v != "" {
			createIn.KmsKeyId = awsString(v)
		}
		if v := intent(req.Intent, "subnet_group"); v != "" {
			createIn.DBSubnetGroupName = awsString(v)
		}
		if sgs := stringListFromIntent(req.Intent, "security_group_ids"); len(sgs) > 0 {
			createIn.VpcSecurityGroupIds = sgs
		}
		if v := intent(req.Intent, "parameter_group"); v != "" {
			createIn.DBParameterGroupName = awsString(v)
		}
		if v := intent(req.Intent, "iops"); v != "" {
			createIn.Iops = awsInt32(parseIntIntent(req.Intent, "iops", 0))
		}
		_, err = c.CreateDBInstance(ctx, createIn)
		if err != nil {
			return nil, fmt.Errorf("rds create db instance: %w", err)
		}
	case "UPDATE":
		class := defaultString(intent(req.Intent, "instance_type"), "db.t3.micro")
		in := &rds.ModifyDBInstanceInput{
			DBInstanceIdentifier: awsString(id),
			ApplyImmediately:     awsBool(true),
			DBInstanceClass:      awsString(class),
		}
		if v := intent(req.Intent, "multi_az"); v != "" {
			in.MultiAZ = awsBool(parseBoolIntent(req.Intent, "multi_az", false))
		}
		if v := intent(req.Intent, "backup_retention"); v != "" {
			in.BackupRetentionPeriod = awsInt32(parseIntIntent(req.Intent, "backup_retention", 7))
		}
		if v := intent(req.Intent, "deletion_protection"); v != "" {
			in.DeletionProtection = awsBool(parseBoolIntent(req.Intent, "deletion_protection", false))
		}
		if s := parseStorageGiB(intent(req.Intent, "disk")); s > 0 {
			in.AllocatedStorage = awsInt32(s)
		}
		if v := intent(req.Intent, "storage_type"); v != "" {
			in.StorageType = awsString(v)
		}
		if v := intent(req.Intent, "backup_window"); v != "" {
			in.PreferredBackupWindow = awsString(v)
		}
		if sgs := stringListFromIntent(req.Intent, "security_group_ids"); len(sgs) > 0 {
			in.VpcSecurityGroupIds = sgs
		}
		if v := intent(req.Intent, "parameter_group"); v != "" {
			in.DBParameterGroupName = awsString(v)
		}
		if v := intent(req.Intent, "iops"); v != "" {
			in.Iops = awsInt32(parseIntIntent(req.Intent, "iops", 0))
		}
		if _, err := c.ModifyDBInstance(ctx, in); err != nil {
			return nil, fmt.Errorf("rds modify db instance: %w", err)
		}
	case "DELETE":
		// Always attempt to disable deletion protection before deleting.
		// This is idempotent — if it wasn't enabled, the call is a no-op.
		if _, err := c.ModifyDBInstance(ctx, &rds.ModifyDBInstanceInput{
			DBInstanceIdentifier: awsString(id),
			DeletionProtection:   awsBool(false),
			ApplyImmediately:     awsBool(true),
		}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("rds disable deletion protection: %w", err)
		}
		_, err := c.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{DBInstanceIdentifier: awsString(id), SkipFinalSnapshot: awsBool(true)})
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("rds delete db instance: %w", err)
		}
	}
	return &ApplyResult{ProviderID: id, LiveState: map[string]interface{}{"service": "rds", "db_instance_id": id, "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSALB(ctx context.Context, c *elbv2.Client, req ApplyRequest) (*ApplyResult, error) {
	name := trimResourceName(identifierFor(req.Action.NodeName), 32)
	arn := req.RecordProviderID()
	switch req.Action.Operation {
	case "CREATE":
		subnets := stringListFromIntent(req.Intent, "subnet_ids")
		if len(subnets) == 0 {
			return nil, fmt.Errorf("alb create requires intent.subnet_ids")
		}
		scheme := defaultString(intent(req.Intent, "scheme"), "internet-facing")
		out, err := c.CreateLoadBalancer(ctx, &elbv2.CreateLoadBalancerInput{
			Name:    awsString(name),
			Scheme:  elbv2types.LoadBalancerSchemeEnum(scheme),
			Subnets: subnets,
			Type:    elbv2types.LoadBalancerTypeEnumApplication,
		})
		if err != nil {
			return nil, fmt.Errorf("elbv2 create load balancer: %w", err)
		}
		if len(out.LoadBalancers) > 0 && out.LoadBalancers[0].LoadBalancerArn != nil {
			arn = *out.LoadBalancers[0].LoadBalancerArn
		}
	case "DELETE":
		if arn == "" {
			return nil, fmt.Errorf("alb delete requires provider id (arn)")
		}
		if _, err := c.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{LoadBalancerArn: awsString(arn)}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("elbv2 delete load balancer: %w", err)
		}
	}
	return &ApplyResult{ProviderID: defaultString(arn, name), LiveState: map[string]interface{}{"service": "elbv2", "load_balancer": defaultString(arn, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSECS(ctx context.Context, c *ecs.Client, req ApplyRequest) (*ApplyResult, error) {
	name := trimResourceName(identifierFor(req.Action.NodeName), 255)
	arn := req.RecordProviderID()
	switch req.Action.Operation {
	case "CREATE":
		out, err := c.CreateCluster(ctx, &ecs.CreateClusterInput{ClusterName: awsString(name)})
		if err != nil {
			return nil, fmt.Errorf("ecs create cluster: %w", err)
		}
		if out.Cluster != nil && out.Cluster.ClusterArn != nil {
			arn = *out.Cluster.ClusterArn
		}
	case "DELETE":
		if _, err := c.DeleteCluster(ctx, &ecs.DeleteClusterInput{Cluster: awsString(defaultString(arn, name))}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("ecs delete cluster: %w", err)
		}
	}
	return &ApplyResult{ProviderID: defaultString(arn, name), LiveState: map[string]interface{}{"service": "ecs", "cluster": defaultString(arn, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSLambda(ctx context.Context, c *lambda.Client, req ApplyRequest) (*ApplyResult, error) {
	name := trimResourceName(identifierFor(req.Action.NodeName), 64)
	arn := req.RecordProviderID()
	switch req.Action.Operation {
	case "CREATE":
		role := intent(req.Intent, "role_arn")
		bucket := intent(req.Intent, "code_s3_bucket")
		key := intent(req.Intent, "code_s3_key")
		if role == "" || bucket == "" || key == "" {
			return nil, fmt.Errorf("lambda create requires intent.role_arn, intent.code_s3_bucket, intent.code_s3_key")
		}
		runtime := defaultString(intent(req.Intent, "runtime"), "provided.al2")
		handler := defaultString(intent(req.Intent, "handler"), "bootstrap")
		createIn := &lambda.CreateFunctionInput{
			FunctionName: awsString(name),
			Role:         awsString(role),
			Runtime:      runtimeFromString(runtime),
			Handler:      awsString(handler),
			Code:         &lambdatypes.FunctionCode{S3Bucket: awsString(bucket), S3Key: awsString(key)},
			MemorySize:   awsInt32(parseIntIntent(req.Intent, "memory", 128)),
			Timeout:      awsInt32(parseIntIntent(req.Intent, "timeout", 30)),
		}
		if env := envFromIntent(req.Intent); env != nil {
			createIn.Environment = &lambdatypes.Environment{Variables: env}
		}
		out, err := c.CreateFunction(ctx, createIn)
		if err != nil {
			return nil, fmt.Errorf("lambda create function: %w", err)
		}
		if out.FunctionArn != nil {
			arn = *out.FunctionArn
		}
	case "UPDATE":
		if arn == "" {
			arn = name
		}
		if bucket := intent(req.Intent, "code_s3_bucket"); bucket != "" {
			key := intent(req.Intent, "code_s3_key")
			if key == "" {
				return nil, fmt.Errorf("lambda update code requires intent.code_s3_key when intent.code_s3_bucket is set")
			}
			if _, err := c.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{FunctionName: awsString(arn), S3Bucket: awsString(bucket), S3Key: awsString(key)}); err != nil {
				return nil, fmt.Errorf("lambda update function code: %w", err)
			}
			// Wait for code update to complete before updating configuration.
			// AWS Lambda rejects UpdateFunctionConfiguration while a code update is in progress.
			waiter := lambda.NewFunctionUpdatedV2Waiter(c)
			if err := waiter.Wait(ctx, &lambda.GetFunctionInput{FunctionName: awsString(arn)}, 2*time.Minute); err != nil {
				return nil, fmt.Errorf("lambda wait for code update: %w", err)
			}
		}
		configIn := &lambda.UpdateFunctionConfigurationInput{
			FunctionName: awsString(arn),
			MemorySize:   awsInt32(parseIntIntent(req.Intent, "memory", 128)),
			Timeout:      awsInt32(parseIntIntent(req.Intent, "timeout", 30)),
		}
		if env := envFromIntent(req.Intent); env != nil {
			configIn.Environment = &lambdatypes.Environment{Variables: env}
		}
		if _, err := c.UpdateFunctionConfiguration(ctx, configIn); err != nil {
			return nil, fmt.Errorf("lambda update function configuration: %w", err)
		}
	case "DELETE":
		if _, err := c.DeleteFunction(ctx, &lambda.DeleteFunctionInput{FunctionName: awsString(defaultString(arn, name))}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("lambda delete function: %w", err)
		}
	}
	return &ApplyResult{ProviderID: defaultString(arn, name), LiveState: map[string]interface{}{"service": "lambda", "function": defaultString(arn, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSAPIGateway(ctx context.Context, c *apigatewayv2.Client, req ApplyRequest) (*ApplyResult, error) {
	name := trimResourceName(identifierFor(req.Action.NodeName), 128)
	apiID := req.RecordProviderID()
	switch req.Action.Operation {
	case "CREATE":
		protocol := defaultString(strings.ToUpper(intent(req.Intent, "protocol")), "HTTP")
		out, err := c.CreateApi(ctx, &apigatewayv2.CreateApiInput{Name: awsString(name), ProtocolType: apigatewayv2types.ProtocolType(protocol)})
		if err != nil {
			return nil, fmt.Errorf("apigatewayv2 create api: %w", err)
		}
		if out.ApiId != nil {
			apiID = *out.ApiId
		}
	case "DELETE":
		if apiID == "" {
			return nil, fmt.Errorf("api gateway delete requires provider id (api id)")
		}
		if _, err := c.DeleteApi(ctx, &apigatewayv2.DeleteApiInput{ApiId: awsString(apiID)}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("apigatewayv2 delete api: %w", err)
		}
	}
	return &ApplyResult{ProviderID: defaultString(apiID, name), LiveState: map[string]interface{}{"service": "apigatewayv2", "api": defaultString(apiID, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSS3(ctx context.Context, c *s3.Client, req ApplyRequest) (*ApplyResult, error) {
	bucket := req.RecordProviderID()
	if bucket == "" {
		bucket = strings.ToLower(strings.ReplaceAll(identifierFor(req.Action.NodeName), "_", "-"))
		bucket = strings.TrimPrefix(bucket, "beecon-")
		bucket = "beecon-" + bucket
	}
	switch req.Action.Operation {
	case "CREATE":
		_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: awsString(bucket)})
		if err != nil {
			return nil, fmt.Errorf("s3 create bucket: %w", err)
		}
		if err := applyS3BucketConfig(ctx, c, bucket, req.Intent); err != nil {
			return nil, fmt.Errorf("s3 bucket created but config failed (bucket %s exists): %w", bucket, err)
		}
	case "DELETE":
		_, err := c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: awsString(bucket)})
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("s3 delete bucket: %w", err)
		}
	case "UPDATE":
		if err := applyS3BucketConfig(ctx, c, bucket, req.Intent); err != nil {
			return nil, fmt.Errorf("s3 update bucket config: %w", err)
		}
	}
	return &ApplyResult{ProviderID: bucket, LiveState: map[string]interface{}{"service": "s3", "bucket": bucket, "operation": req.Action.Operation}}, nil
}

// applyS3BucketConfig applies versioning, encryption, and lifecycle config to an S3 bucket.
func applyS3BucketConfig(ctx context.Context, c *s3.Client, bucket string, intentMap map[string]interface{}) error {
	if parseBoolIntent(intentMap, "versioning", false) {
		if _, err := c.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket: awsString(bucket),
			VersioningConfiguration: &s3types.VersioningConfiguration{
				Status: s3types.BucketVersioningStatusEnabled,
			},
		}); err != nil {
			return fmt.Errorf("s3 put bucket versioning: %w", err)
		}
	}
	kmsKey := intent(intentMap, "kms_key")
	encryption := intent(intentMap, "encryption")
	// Only apply encryption config when explicitly requested or on first setup (kms_key or encryption intent present).
	if kmsKey != "" || encryption != "" {
		encAlgo := s3types.ServerSideEncryptionAes256
		var kmsKeyID *string
		if kmsKey != "" {
			encAlgo = s3types.ServerSideEncryptionAwsKms
			kmsKeyID = awsString(kmsKey)
		}
		encRule := s3types.ServerSideEncryptionRule{
			ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
				SSEAlgorithm:   encAlgo,
				KMSMasterKeyID: kmsKeyID,
			},
		}
		if _, err := c.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
			Bucket: awsString(bucket),
			ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
				Rules: []s3types.ServerSideEncryptionRule{encRule},
			},
		}); err != nil {
			return fmt.Errorf("s3 put bucket encryption: %w", err)
		}
	}
	if days := parseIntIntent(intentMap, "lifecycle_days", 0); days > 0 {
		if _, err := c.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
			Bucket: awsString(bucket),
			LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{
				Rules: []s3types.LifecycleRule{
					{
						ID:     awsString("beecon-expiration"),
						Status: s3types.ExpirationStatusEnabled,
						Filter: &s3types.LifecycleRuleFilter{Prefix: awsString("")},
						Expiration: &s3types.LifecycleExpiration{
							Days: awsInt32(days),
						},
					},
				},
			},
		}); err != nil {
			return fmt.Errorf("s3 put bucket lifecycle: %w", err)
		}
	}
	return nil
}

func (e *DefaultExecutor) applyAWSSQS(ctx context.Context, c *sqs.Client, req ApplyRequest) (*ApplyResult, error) {
	name := identifierFor(req.Action.NodeName)
	url := req.RecordProviderID()
	switch req.Action.Operation {
	case "CREATE":
		out, err := c.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: awsString(name)})
		if err != nil {
			return nil, fmt.Errorf("sqs create queue: %w", err)
		}
		if out.QueueUrl != nil {
			url = *out.QueueUrl
		}
	case "DELETE":
		if url == "" {
			out, err := c.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: awsString(name)})
			if err == nil && out.QueueUrl != nil {
				url = *out.QueueUrl
			}
		}
		if url != "" {
			if _, err := c.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: awsString(url)}); err != nil && !isNotFound(err) {
				return nil, fmt.Errorf("sqs delete queue: %w", err)
			}
		}
	}
	return &ApplyResult{ProviderID: defaultString(url, name), LiveState: map[string]interface{}{"service": "sqs", "queue": defaultString(url, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSSNS(ctx context.Context, c *sns.Client, req ApplyRequest) (*ApplyResult, error) {
	name := identifierFor(req.Action.NodeName)
	arn := req.RecordProviderID()
	switch req.Action.Operation {
	case "CREATE":
		out, err := c.CreateTopic(ctx, &sns.CreateTopicInput{Name: awsString(name)})
		if err != nil {
			return nil, fmt.Errorf("sns create topic: %w", err)
		}
		if out.TopicArn != nil {
			arn = *out.TopicArn
		}
	case "DELETE":
		if arn != "" {
			if _, err := c.DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: awsString(arn)}); err != nil && !isNotFound(err) {
				return nil, fmt.Errorf("sns delete topic: %w", err)
			}
		}
	}
	return &ApplyResult{ProviderID: defaultString(arn, name), LiveState: map[string]interface{}{"service": "sns", "topic": defaultString(arn, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSSecrets(ctx context.Context, c *secretsmanager.Client, req ApplyRequest) (*ApplyResult, error) {
	name := identifierFor(req.Action.NodeName)
	id := req.RecordProviderID()
	secret := intent(req.Intent, "secret_value", "password")
	switch req.Action.Operation {
	case "CREATE":
		if secret == "" {
			return nil, fmt.Errorf("secretsmanager create requires intent.secret_value or intent.password")
		}
		createIn := &secretsmanager.CreateSecretInput{Name: awsString(name), SecretString: awsString(secret)}
		if v := intent(req.Intent, "kms_key"); v != "" {
			createIn.KmsKeyId = awsString(v)
		}
		if v := intent(req.Intent, "description"); v != "" {
			createIn.Description = awsString(v)
		}
		out, err := c.CreateSecret(ctx, createIn)
		if err != nil {
			return nil, fmt.Errorf("secretsmanager create secret: %w", err)
		}
		if out.ARN != nil {
			id = *out.ARN
		}
	case "UPDATE":
		if secret == "" {
			return nil, fmt.Errorf("secretsmanager update requires intent.secret_value or intent.password")
		}
		if _, err := c.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{SecretId: awsString(defaultString(id, name)), SecretString: awsString(secret)}); err != nil {
			return nil, fmt.Errorf("secretsmanager put secret value: %w", err)
		}
	case "DELETE":
		if _, err := c.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{SecretId: awsString(defaultString(id, name)), ForceDeleteWithoutRecovery: awsBool(true)}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("secretsmanager delete secret: %w", err)
		}
	}
	return &ApplyResult{ProviderID: defaultString(id, name), LiveState: map[string]interface{}{"service": "secretsmanager", "secret": defaultString(id, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSIAM(ctx context.Context, c *iam.Client, req ApplyRequest) (*ApplyResult, error) {
	name := identifierFor(req.Action.NodeName)
	id := req.RecordProviderID()
	trustService := intent(req.Intent, "trust_service")
	if trustService == "" {
		trustService = detectTrustService(req.Intent)
	}
	trust, err := trustPolicyForService(trustService)
	if err != nil {
		return nil, fmt.Errorf("iam trust policy: %w", err)
	}

	managedPolicies := stringListFromIntent(req.Intent, "managed_policies")
	for _, p := range managedPolicies {
		if !strings.HasPrefix(p, "arn:") {
			return nil, fmt.Errorf("managed_policies entry %q must start with arn:", p)
		}
	}

	switch req.Action.Operation {
	case "CREATE":
		out, err := c.CreateRole(ctx, &iam.CreateRoleInput{RoleName: awsString(name), AssumeRolePolicyDocument: awsString(trust)})
		if err != nil {
			return nil, fmt.Errorf("iam create role: %w", err)
		}
		if out.Role != nil && out.Role.Arn != nil {
			id = *out.Role.Arn
		}
		for _, policyArn := range managedPolicies {
			if _, err := c.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
				RoleName:  awsString(name),
				PolicyArn: awsString(policyArn),
			}); err != nil {
				return nil, fmt.Errorf("iam attach role policy %s: %w", policyArn, err)
			}
		}
	case "UPDATE":
		if _, err := c.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
			RoleName:       awsString(name),
			PolicyDocument: awsString(trust),
		}); err != nil {
			return nil, fmt.Errorf("iam update assume role policy: %w", err)
		}
		// Diff attached policies: list current → detach removed → attach new
		currentPolicies, err := listAttachedPolicies(ctx, c, name)
		if err != nil {
			return nil, err
		}
		desiredSet := make(map[string]bool, len(managedPolicies))
		for _, p := range managedPolicies {
			desiredSet[p] = true
		}
		currentSet := make(map[string]bool, len(currentPolicies))
		for _, p := range currentPolicies {
			currentSet[p] = true
		}
		for _, p := range currentPolicies {
			if !desiredSet[p] {
				if _, err := c.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{RoleName: awsString(name), PolicyArn: awsString(p)}); err != nil && !isNotFound(err) {
					return nil, fmt.Errorf("iam detach role policy %s: %w", p, err)
				}
			}
		}
		for _, p := range managedPolicies {
			if !currentSet[p] {
				if _, err := c.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{RoleName: awsString(name), PolicyArn: awsString(p)}); err != nil {
					return nil, fmt.Errorf("iam attach role policy %s: %w", p, err)
				}
			}
		}
	case "DELETE":
		// Detach all policies before deleting the role
		policies, err := listAttachedPolicies(ctx, c, name)
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		for _, p := range policies {
			if _, err := c.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{RoleName: awsString(name), PolicyArn: awsString(p)}); err != nil && !isNotFound(err) {
				return nil, fmt.Errorf("iam detach role policy before delete %s: %w", p, err)
			}
		}
		if _, err := c.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: awsString(name)}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("iam delete role: %w", err)
		}
	}
	return &ApplyResult{ProviderID: defaultString(id, name), LiveState: map[string]interface{}{"service": "iam", "role": defaultString(id, name), "operation": req.Action.Operation}}, nil
}

// listAttachedPolicies returns all policy ARNs attached to an IAM role,
// paginating through all results.
func listAttachedPolicies(ctx context.Context, c *iam.Client, roleName string) ([]string, error) {
	var arns []string
	paginator := iam.NewListAttachedRolePoliciesPaginator(c, &iam.ListAttachedRolePoliciesInput{RoleName: awsString(roleName)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("iam list attached role policies: %w", err)
		}
		for _, p := range page.AttachedPolicies {
			if p.PolicyArn != nil {
				arns = append(arns, *p.PolicyArn)
			}
		}
	}
	return arns, nil
}

func (e *DefaultExecutor) applyAWSEC2(ctx context.Context, c *ec2.Client, req ApplyRequest, target string) (*ApplyResult, error) {
	name := identifierFor(req.Action.NodeName)
	id := req.RecordProviderID()
	switch target {
	case "vpc":
		switch req.Action.Operation {
		case "CREATE":
			cidr := defaultString(intent(req.Intent, "cidr"), "10.0.0.0/16")
			out, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: awsString(cidr)})
			if err != nil {
				return nil, fmt.Errorf("ec2 create vpc: %w", err)
			}
			if out.Vpc != nil && out.Vpc.VpcId != nil {
				id = *out.Vpc.VpcId
			}
		case "DELETE":
			if id == "" {
				return nil, fmt.Errorf("vpc delete requires provider id")
			}
			if _, err := c.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: awsString(id)}); err != nil && !isNotFound(err) {
				return nil, fmt.Errorf("ec2 delete vpc: %w", err)
			}
		}
	case "subnet":
		switch req.Action.Operation {
		case "CREATE":
			vpcID := intent(req.Intent, "vpc_id")
			if vpcID == "" {
				return nil, fmt.Errorf("subnet create requires intent.vpc_id")
			}
			cidr := defaultString(intent(req.Intent, "cidr"), "10.0.1.0/24")
			out, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{VpcId: awsString(vpcID), CidrBlock: awsString(cidr)})
			if err != nil {
				return nil, fmt.Errorf("ec2 create subnet: %w", err)
			}
			if out.Subnet != nil && out.Subnet.SubnetId != nil {
				id = *out.Subnet.SubnetId
			}
		case "DELETE":
			if id == "" {
				return nil, fmt.Errorf("subnet delete requires provider id")
			}
			if _, err := c.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: awsString(id)}); err != nil && !isNotFound(err) {
				return nil, fmt.Errorf("ec2 delete subnet: %w", err)
			}
		}
	case "security_group":
		desc := defaultString(intent(req.Intent, "description"), "beecon managed security group")
		switch req.Action.Operation {
		case "CREATE":
			vpcID := intent(req.Intent, "vpc_id")
			if vpcID == "" {
				return nil, fmt.Errorf("security group create requires intent.vpc_id")
			}
			out, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{VpcId: awsString(vpcID), GroupName: awsString(name), Description: awsString(desc)})
			if err != nil {
				return nil, fmt.Errorf("ec2 create security group: %w", err)
			}
			if out.GroupId != nil {
				id = *out.GroupId
			}
			if err := applySGRules(ctx, c, id, req.Intent); err != nil {
				return nil, err
			}
		case "UPDATE":
			if id == "" {
				return nil, fmt.Errorf("security group update requires provider id")
			}
			// Safe update: authorize new rules first (additive), then revoke stale rules.
			// This avoids a window where the SG has no rules at all.
			descOut, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{id}})
			if err != nil {
				return nil, fmt.Errorf("ec2 describe security group for update: %w", err)
			}
			// Authorize new rules first (duplicates are idempotent in AWS)
			if err := applySGRules(ctx, c, id, req.Intent); err != nil {
				return nil, err
			}
			// Now revoke old rules that aren't in the new set
			if len(descOut.SecurityGroups) > 0 {
				sg := descOut.SecurityGroups[0]
				newIngress, err := buildNewPermsFromIntent(req.Intent, "ingress")
				if err != nil {
					return nil, err
				}
				newEgress, err := buildNewPermsFromIntent(req.Intent, "egress")
				if err != nil {
					return nil, err
				}
				if stale := diffIPPermissions(sg.IpPermissions, newIngress); len(stale) > 0 {
					if _, err := c.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{GroupId: awsString(id), IpPermissions: stale}); err != nil {
						return nil, fmt.Errorf("ec2 revoke stale ingress: %w", err)
					}
				}
				if stale := diffIPPermissions(sg.IpPermissionsEgress, newEgress); len(stale) > 0 {
					if _, err := c.RevokeSecurityGroupEgress(ctx, &ec2.RevokeSecurityGroupEgressInput{GroupId: awsString(id), IpPermissions: stale}); err != nil {
						return nil, fmt.Errorf("ec2 revoke stale egress: %w", err)
					}
				}
			}
		case "DELETE":
			if id == "" {
				return nil, fmt.Errorf("security group delete requires provider id")
			}
			if _, err := c.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: awsString(id)}); err != nil && !isNotFound(err) {
				return nil, fmt.Errorf("ec2 delete security group: %w", err)
			}
		}
	case "ec2":
		switch req.Action.Operation {
		case "CREATE":
			imageID := defaultString(intent(req.Intent, "ami", "image_id"), "ami-0c02fb55956c7d316")
			instanceType := defaultString(intent(req.Intent, "instance_type"), "t3.micro")
			in := &ec2.RunInstancesInput{
				ImageId:      awsString(imageID),
				InstanceType: ec2types.InstanceType(instanceType),
				MinCount:     awsInt32(1),
				MaxCount:     awsInt32(1),
			}
			if subnetID := intent(req.Intent, "subnet_id"); subnetID != "" {
				in.SubnetId = awsString(subnetID)
			}
			if sgs := stringListFromIntent(req.Intent, "security_group_ids"); len(sgs) > 0 {
				in.SecurityGroupIds = sgs
			}
			out, err := c.RunInstances(ctx, in)
			if err != nil {
				return nil, fmt.Errorf("ec2 run instances: %w", err)
			}
			if len(out.Instances) > 0 && out.Instances[0].InstanceId != nil {
				id = *out.Instances[0].InstanceId
			}
		case "UPDATE":
			if id == "" {
				return nil, fmt.Errorf("ec2 update requires provider id")
			}
			// NOTE: Changing instance type requires the instance to be stopped.
			// AWS will return IncorrectInstanceState if the instance is running.
			instanceType := defaultString(intent(req.Intent, "instance_type"), "")
			if instanceType != "" {
				_, err := c.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
					InstanceId: awsString(id),
					InstanceType: &ec2types.AttributeValue{
						Value: awsString(instanceType),
					},
				})
				if err != nil {
					return nil, fmt.Errorf("ec2 modify instance attribute: %w", err)
				}
			}
		case "DELETE":
			if id == "" {
				return nil, fmt.Errorf("ec2 delete requires provider id")
			}
			if _, err := c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil && !isNotFound(err) {
				return nil, fmt.Errorf("ec2 terminate instances: %w", err)
			}
		}
	}
	return &ApplyResult{ProviderID: defaultString(id, name), LiveState: map[string]interface{}{"service": "ec2", "resource": target, "id": defaultString(id, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSElastiCache(ctx context.Context, c *elasticache.Client, req ApplyRequest) (*ApplyResult, error) {
	id := trimResourceName(identifierFor(req.Action.NodeName), 50)
	switch req.Action.Operation {
	case "CREATE":
		engine := defaultString(intent(req.Intent, "engine", "type"), "redis")
		nodeType := defaultString(intent(req.Intent, "node_type"), "cache.t3.micro")
		numNodes := int32(1)
		if v := strings.TrimSpace(intent(req.Intent, "num_cache_nodes")); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
				numNodes = int32(parsed)
			}
		}
		createIn := &elasticache.CreateCacheClusterInput{
			CacheClusterId: awsString(id),
			Engine:         awsString(engine),
			CacheNodeType:  awsString(nodeType),
			NumCacheNodes:  awsInt32(numNodes),
		}
		if v := intent(req.Intent, "parameter_group"); v != "" {
			createIn.CacheParameterGroupName = awsString(v)
		}
		if v := intent(req.Intent, "subnet_group"); v != "" {
			createIn.CacheSubnetGroupName = awsString(v)
		}
		if sgs := stringListFromIntent(req.Intent, "security_group_ids"); len(sgs) > 0 {
			createIn.SecurityGroupIds = sgs
		}
		if v := intent(req.Intent, "auth_token"); v != "" {
			createIn.AuthToken = awsString(v)
			createIn.TransitEncryptionEnabled = awsBool(true)
		}
		if v := parseIntIntent(req.Intent, "snapshot_retention", 0); v > 0 {
			createIn.SnapshotRetentionLimit = awsInt32(v)
		}
		_, err := c.CreateCacheCluster(ctx, createIn)
		if err != nil {
			return nil, fmt.Errorf("elasticache create cache cluster: %w", err)
		}
	case "UPDATE":
		in := &elasticache.ModifyCacheClusterInput{
			CacheClusterId:   awsString(id),
			ApplyImmediately: awsBool(true),
		}
		if nodeType := strings.TrimSpace(intent(req.Intent, "node_type")); nodeType != "" {
			in.CacheNodeType = awsString(nodeType)
		}
		if v := strings.TrimSpace(intent(req.Intent, "num_cache_nodes")); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
				in.NumCacheNodes = awsInt32(int32(parsed))
			}
		}
		if v := intent(req.Intent, "parameter_group"); v != "" {
			in.CacheParameterGroupName = awsString(v)
		}
		if sgs := stringListFromIntent(req.Intent, "security_group_ids"); len(sgs) > 0 {
			in.SecurityGroupIds = sgs
		}
		if v := parseIntIntent(req.Intent, "snapshot_retention", 0); v > 0 {
			in.SnapshotRetentionLimit = awsInt32(v)
		}
		if v := intent(req.Intent, "auth_token"); v != "" {
			in.AuthToken = awsString(v)
			in.AuthTokenUpdateStrategy = elasticachetypes.AuthTokenUpdateStrategyTypeRotate
		}
		if _, err := c.ModifyCacheCluster(ctx, in); err != nil {
			return nil, fmt.Errorf("elasticache modify cache cluster: %w", err)
		}
	case "DELETE":
		if _, err := c.DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{CacheClusterId: awsString(id)}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("elasticache delete cache cluster: %w", err)
		}
	}
	return &ApplyResult{ProviderID: id, LiveState: map[string]interface{}{"service": "elasticache", "cache_cluster_id": id, "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSCloudFront(ctx context.Context, c *cloudfront.Client, req ApplyRequest) (*ApplyResult, error) {
	id := req.RecordProviderID()
	switch req.Action.Operation {
	case "CREATE":
		cfg, err := cloudFrontDistributionConfigFromIntent(req.Intent, identifierFor(req.Action.NodeName), "")
		if err != nil {
			return nil, err
		}
		out, err := c.CreateDistribution(ctx, &cloudfront.CreateDistributionInput{DistributionConfig: cfg})
		if err != nil {
			return nil, fmt.Errorf("cloudfront create distribution: %w", err)
		}
		if out.Distribution != nil && out.Distribution.Id != nil {
			id = *out.Distribution.Id
		}
	case "UPDATE":
		if id == "" {
			return nil, fmt.Errorf("cloudfront update requires provider id")
		}
		current, err := c.GetDistributionConfig(ctx, &cloudfront.GetDistributionConfigInput{Id: awsString(id)})
		if err != nil {
			return nil, fmt.Errorf("cloudfront get distribution config: %w", err)
		}
		cfg, err := cloudFrontDistributionConfigFromIntent(req.Intent, identifierFor(req.Action.NodeName), "")
		if err != nil {
			return nil, err
		}
		if _, err := c.UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
			Id:                 awsString(id),
			IfMatch:            current.ETag,
			DistributionConfig: cfg,
		}); err != nil {
			return nil, fmt.Errorf("cloudfront update distribution: %w", err)
		}
	case "DELETE":
		if id == "" {
			return nil, fmt.Errorf("cloudfront delete requires provider id")
		}
		current, err := c.GetDistributionConfig(ctx, &cloudfront.GetDistributionConfigInput{Id: awsString(id)})
		if err != nil {
			if isNotFound(err) {
				return &ApplyResult{ProviderID: id, LiveState: map[string]interface{}{"service": "cloudfront", "distribution_id": id, "operation": req.Action.Operation}}, nil
			}
			return nil, fmt.Errorf("cloudfront get distribution config: %w", err)
		}
		cfg := current.DistributionConfig
		if cfg != nil && cfg.Enabled != nil && *cfg.Enabled {
			cfg.Enabled = awsBool(false)
			updated, err := c.UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
				Id:                 awsString(id),
				IfMatch:            current.ETag,
				DistributionConfig: cfg,
			})
			if err != nil {
				return nil, fmt.Errorf("cloudfront disable distribution before delete: %w", err)
			}
			if updated != nil && updated.ETag != nil {
				current.ETag = updated.ETag
			}
		}
		if _, err := c.DeleteDistribution(ctx, &cloudfront.DeleteDistributionInput{Id: awsString(id), IfMatch: current.ETag}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("cloudfront delete distribution: %w", err)
		}
	}
	return &ApplyResult{ProviderID: id, LiveState: map[string]interface{}{"service": "cloudfront", "distribution_id": id, "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSRoute53(ctx context.Context, c *route53.Client, req ApplyRequest) (*ApplyResult, error) {
	id := req.RecordProviderID()
	name := defaultString(intent(req.Intent, "domain", "zone_name"), strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")+".example.com")
	switch req.Action.Operation {
	case "CREATE":
		out, err := c.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
			Name:            awsString(name),
			CallerReference: awsString(state.NewID("beecon-zone")),
		})
		if err != nil {
			return nil, fmt.Errorf("route53 create hosted zone: %w", err)
		}
		if out.HostedZone != nil && out.HostedZone.Id != nil {
			id = *out.HostedZone.Id
		}
	case "DELETE":
		if id == "" {
			return nil, fmt.Errorf("route53 delete requires provider id (hosted zone id)")
		}
		if _, err := c.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{Id: awsString(id)}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("route53 delete hosted zone: %w", err)
		}
	}
	return &ApplyResult{ProviderID: id, LiveState: map[string]interface{}{"service": "route53", "hosted_zone_id": id, "domain": name, "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSCloudWatch(ctx context.Context, c *cloudwatch.Client, req ApplyRequest) (*ApplyResult, error) {
	name := trimResourceName(identifierFor(req.Action.NodeName), 255)
	if v := strings.TrimSpace(intent(req.Intent, "alarm_name")); v != "" {
		name = v
	}
	switch req.Action.Operation {
	case "CREATE", "UPDATE":
		threshold := 80.0
		if raw := strings.TrimSpace(intent(req.Intent, "threshold")); raw != "" {
			if p, err := strconv.ParseFloat(raw, 64); err == nil {
				threshold = p
			}
		}
		period := int32(60)
		if raw := strings.TrimSpace(intent(req.Intent, "period_seconds")); raw != "" {
			if p, err := strconv.Atoi(raw); err == nil && p > 0 {
				period = int32(p)
			}
		}
		evals := int32(1)
		if raw := strings.TrimSpace(intent(req.Intent, "evaluation_periods")); raw != "" {
			if p, err := strconv.Atoi(raw); err == nil && p > 0 {
				evals = int32(p)
			}
		}
		comparison := defaultString(strings.ToUpper(intent(req.Intent, "comparison_operator")), "GREATER_THAN_OR_EQUAL_TO_THRESHOLD")
		stat := cloudWatchStatisticFromString(intent(req.Intent, "statistic"))
		if _, err := c.PutMetricAlarm(ctx, &cloudwatch.PutMetricAlarmInput{
			AlarmName:          awsString(name),
			MetricName:         awsString(defaultString(intent(req.Intent, "metric_name"), "CPUUtilization")),
			Namespace:          awsString(defaultString(intent(req.Intent, "namespace"), "AWS/EC2")),
			ComparisonOperator: cloudwatchtypes.ComparisonOperator(comparison),
			EvaluationPeriods:  awsInt32(evals),
			Period:             awsInt32(period),
			Threshold:          awsFloat64(threshold),
			Statistic:          stat,
		}); err != nil {
			return nil, fmt.Errorf("cloudwatch put metric alarm: %w", err)
		}
	case "DELETE":
		if _, err := c.DeleteAlarms(ctx, &cloudwatch.DeleteAlarmsInput{AlarmNames: []string{name}}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("cloudwatch delete alarms: %w", err)
		}
	}
	return &ApplyResult{ProviderID: name, LiveState: map[string]interface{}{"service": "cloudwatch", "alarm_name": name, "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSEKS(ctx context.Context, c *eks.Client, req ApplyRequest) (*ApplyResult, error) {
	name := trimResourceName(identifierFor(req.Action.NodeName), 100)
	switch req.Action.Operation {
	case "CREATE":
		roleArn := intent(req.Intent, "role_arn")
		subnets := stringListFromIntent(req.Intent, "subnet_ids")
		if roleArn == "" || len(subnets) == 0 {
			return nil, fmt.Errorf("eks create requires intent.role_arn and intent.subnet_ids")
		}
		_, err := c.CreateCluster(ctx, &eks.CreateClusterInput{
			Name:    awsString(name),
			RoleArn: awsString(roleArn),
			ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
				SubnetIds: subnets,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("eks create cluster: %w", err)
		}
	case "DELETE":
		if _, err := c.DeleteCluster(ctx, &eks.DeleteClusterInput{Name: awsString(name)}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("eks delete cluster: %w", err)
		}
	}
	return &ApplyResult{ProviderID: name, LiveState: map[string]interface{}{"service": "eks", "cluster_name": name, "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSEventBridge(ctx context.Context, c *eventbridge.Client, req ApplyRequest) (*ApplyResult, error) {
	name := trimResourceName(identifierFor(req.Action.NodeName), 64)
	switch req.Action.Operation {
	case "CREATE", "UPDATE":
		in := &eventbridge.PutRuleInput{
			Name:  awsString(name),
			State: eventbridgetypes.RuleStateEnabled,
		}
		if schedule := intent(req.Intent, "schedule_expression"); schedule != "" {
			in.ScheduleExpression = awsString(schedule)
		}
		if pattern := intent(req.Intent, "event_pattern"); pattern != "" {
			in.EventPattern = awsString(pattern)
		}
		if _, err := c.PutRule(ctx, in); err != nil {
			return nil, fmt.Errorf("eventbridge put rule: %w", err)
		}
	case "DELETE":
		if _, err := c.DeleteRule(ctx, &eventbridge.DeleteRuleInput{Name: awsString(name), Force: true}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("eventbridge delete rule: %w", err)
		}
	}
	return &ApplyResult{ProviderID: name, LiveState: map[string]interface{}{"service": "eventbridge", "rule_name": name, "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) applyAWSCognito(ctx context.Context, c *cognitoidentityprovider.Client, req ApplyRequest) (*ApplyResult, error) {
	id := req.RecordProviderID()
	name := trimResourceName(identifierFor(req.Action.NodeName), 128)
	switch req.Action.Operation {
	case "CREATE":
		out, err := c.CreateUserPool(ctx, &cognitoidentityprovider.CreateUserPoolInput{
			PoolName: awsString(name),
		})
		if err != nil {
			return nil, fmt.Errorf("cognito create user pool: %w", err)
		}
		if out.UserPool != nil && out.UserPool.Id != nil {
			id = *out.UserPool.Id
		}
	case "UPDATE":
		if id == "" {
			return nil, fmt.Errorf("cognito update requires provider id")
		}
		_, err := c.UpdateUserPool(ctx, &cognitoidentityprovider.UpdateUserPoolInput{UserPoolId: awsString(id)})
		if err != nil {
			return nil, fmt.Errorf("cognito update user pool: %w", err)
		}
	case "DELETE":
		if id == "" {
			return nil, fmt.Errorf("cognito delete requires provider id")
		}
		if _, err := c.DeleteUserPool(ctx, &cognitoidentityprovider.DeleteUserPoolInput{UserPoolId: awsString(id)}); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("cognito delete user pool: %w", err)
		}
	}
	return &ApplyResult{ProviderID: defaultString(id, name), LiveState: map[string]interface{}{"service": "cognito", "user_pool_id": defaultString(id, name), "operation": req.Action.Operation}}, nil
}

func (e *DefaultExecutor) observeAWS(ctx context.Context, region string, rec *state.ResourceRecord) (*ObserveResult, error) {
	if e.dryRun {
		if rec == nil {
			return &ObserveResult{Exists: false, LiveState: map[string]interface{}{}}, nil
		}
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	if rec == nil {
		return &ObserveResult{Exists: false, LiveState: map[string]interface{}{}}, nil
	}
	target := detectRecordTarget(rec)
	switch target {
	case "rds":
		id := defaultString(rec.ProviderID, identifierFor(rec.NodeName))
		c := rds.NewFromConfig(cfg)
		out, err := c.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{DBInstanceIdentifier: awsString(id)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("rds describe db instance: %w", err)
		}
		if len(out.DBInstances) == 0 {
			return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
		}
		db := out.DBInstances[0]
		live := map[string]interface{}{
			"service":              "rds",
			"status":              stringValue(db.DBInstanceStatus),
			"engine":              stringValue(db.Engine),
			"instance_type":       stringValue(db.DBInstanceClass),
			"allocated_storage_gb": intValue(db.AllocatedStorage),
			"storage_type": stringValue(db.StorageType),
		}
		if db.DeletionProtection != nil {
			live["deletion_protection"] = *db.DeletionProtection
		}
		if db.MultiAZ != nil {
			live["multi_az"] = *db.MultiAZ
		}
		if db.BackupRetentionPeriod != nil {
			live["backup_retention_period"] = *db.BackupRetentionPeriod
		}
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: live}, nil
	case "s3":
		bucket := rec.ProviderID
		if bucket == "" {
			bucket = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
			bucket = "beecon-" + bucket
		}
		c := s3.NewFromConfig(cfg)
		_, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: awsString(bucket)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: bucket, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("s3 head bucket: %w", err)
		}
		live := map[string]interface{}{"service": "s3", "bucket": bucket}
		if vOut, err := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: awsString(bucket)}); err == nil {
			live["versioning"] = string(vOut.Status) == "Enabled"
		}
		if eOut, err := c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: awsString(bucket)}); err == nil && eOut.ServerSideEncryptionConfiguration != nil {
			for _, rule := range eOut.ServerSideEncryptionConfiguration.Rules {
				if rule.ApplyServerSideEncryptionByDefault != nil {
					live["encryption"] = string(rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm)
					if rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID != nil {
						live["kms_key_id"] = *rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID
					}
					break
				}
			}
		}
		if lcOut, err := c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: awsString(bucket)}); err == nil {
			for _, rule := range lcOut.Rules {
				if rule.Expiration != nil && rule.Expiration.Days != nil {
					live["lifecycle_days"] = *rule.Expiration.Days
					break
				}
			}
		}
		return &ObserveResult{Exists: true, ProviderID: bucket, LiveState: live}, nil
	case "sqs":
		q := sqs.NewFromConfig(cfg)
		url := rec.ProviderID
		if url == "" {
			out, err := q.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: awsString(identifierFor(rec.NodeName))})
			if err != nil {
				if isNotFound(err) {
					return &ObserveResult{Exists: false, ProviderID: "", LiveState: map[string]interface{}{}}, nil
				}
				return nil, fmt.Errorf("sqs get queue url: %w", err)
			}
			if out.QueueUrl != nil {
				url = *out.QueueUrl
			}
		}
		if url == "" {
			return &ObserveResult{Exists: false, ProviderID: "", LiveState: map[string]interface{}{}}, nil
		}
		attrOut, err := q.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       awsString(url),
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameApproximateNumberOfMessages},
		})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: url, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("sqs get queue attributes: %w", err)
		}
		return &ObserveResult{Exists: true, ProviderID: url, LiveState: map[string]interface{}{"service": "sqs", "queue_url": url, "attributes": attrOut.Attributes}}, nil
	case "sns":
		if rec.ProviderID == "" {
			return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
		}
		s := sns.NewFromConfig(cfg)
		_, err := s.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: awsString(rec.ProviderID)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: rec.ProviderID, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("sns get topic attributes: %w", err)
		}
		return &ObserveResult{Exists: true, ProviderID: rec.ProviderID, LiveState: map[string]interface{}{"service": "sns", "topic_arn": rec.ProviderID}}, nil
	case "secrets_manager":
		id := rec.ProviderID
		if id == "" {
			id = identifierFor(rec.NodeName)
		}
		s := secretsmanager.NewFromConfig(cfg)
		out, err := s.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: awsString(id)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("secretsmanager describe secret: %w", err)
		}
		live := map[string]interface{}{"service": "secretsmanager", "secret": id}
		if out.Name != nil {
			live["name"] = *out.Name
		}
		if out.KmsKeyId != nil {
			live["kms_key_id"] = *out.KmsKeyId
		}
		if out.Description != nil {
			live["description"] = *out.Description
		}
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: live}, nil
	case "iam":
		roleName := identifierFor(rec.NodeName)
		s := iam.NewFromConfig(cfg)
		out, err := s.GetRole(ctx, &iam.GetRoleInput{RoleName: awsString(roleName)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: rec.ProviderID, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("iam get role: %w", err)
		}
		arn := ""
		if out.Role != nil && out.Role.Arn != nil {
			arn = *out.Role.Arn
		}
		live := map[string]interface{}{"service": "iam", "role_name": roleName, "arn": arn}
		if policyArns, err := listAttachedPolicies(ctx, s, roleName); err == nil {
			live["attached_policies"] = policyArns
		}
		// Extract trust_service from assume role policy document.
		if out.Role != nil && out.Role.AssumeRolePolicyDocument != nil {
			if decoded, err := url.QueryUnescape(*out.Role.AssumeRolePolicyDocument); err == nil {
				var doc struct {
					Statement []struct {
						Principal struct {
							Service interface{} `json:"Service"`
						} `json:"Principal"`
					} `json:"Statement"`
				}
				if err := json.Unmarshal([]byte(decoded), &doc); err == nil && len(doc.Statement) > 0 {
					switch v := doc.Statement[0].Principal.Service.(type) {
					case string:
						live["trust_service"] = v
					case []interface{}:
						if len(v) > 0 {
							live["trust_service"] = fmt.Sprint(v[0])
						}
					}
				}
			}
		}
		return &ObserveResult{Exists: true, ProviderID: defaultString(rec.ProviderID, arn), LiveState: live}, nil
	case "vpc":
		id := rec.ProviderID
		if id == "" {
			return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
		}
		c := ec2.NewFromConfig(cfg)
		out, err := c.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{id}})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("ec2 describe vpcs: %w", err)
		}
		if len(out.Vpcs) == 0 {
			return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
		}
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: map[string]interface{}{"service": "ec2", "resource": "vpc", "id": id}}, nil
	case "subnet":
		id := rec.ProviderID
		if id == "" {
			return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
		}
		c := ec2.NewFromConfig(cfg)
		out, err := c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{id}})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("ec2 describe subnets: %w", err)
		}
		if len(out.Subnets) == 0 {
			return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
		}
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: map[string]interface{}{"service": "ec2", "resource": "subnet", "id": id}}, nil
	case "security_group":
		id := rec.ProviderID
		if id == "" {
			return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
		}
		c := ec2.NewFromConfig(cfg)
		out, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{id}})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("ec2 describe security groups: %w", err)
		}
		if len(out.SecurityGroups) == 0 {
			return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
		}
		sg := out.SecurityGroups[0]
		sgLive := map[string]interface{}{"service": "ec2", "resource": "security_group", "id": id}
		sgLive["ingress"] = serializeSGRules(sg.IpPermissions)
		sgLive["egress"] = serializeSGRules(sg.IpPermissionsEgress)
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: sgLive}, nil
	case "ec2":
		id := rec.ProviderID
		if id == "" {
			return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
		}
		c := ec2.NewFromConfig(cfg)
		out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("ec2 describe instances: %w", err)
		}
		if len(out.Reservations) == 0 || len(out.Reservations[0].Instances) == 0 {
			return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
		}
		inst := out.Reservations[0].Instances[0]
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: map[string]interface{}{"service": "ec2", "resource": "ec2", "id": id, "state": string(inst.State.Name), "instance_type": string(inst.InstanceType)}}, nil
	case "elasticache":
		id := rec.ProviderID
		if id == "" {
			id = trimResourceName(identifierFor(rec.NodeName), 50)
		}
		c := elasticache.NewFromConfig(cfg)
		out, err := c.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{CacheClusterId: awsString(id)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("elasticache describe cache cluster: %w", err)
		}
		if len(out.CacheClusters) == 0 {
			return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
		}
		cluster := out.CacheClusters[0]
		ecLive := map[string]interface{}{
			"service":          "elasticache",
			"cache_cluster_id": id,
			"status":           stringValue(cluster.CacheClusterStatus),
			"engine":           stringValue(cluster.Engine),
			"node_type":        stringValue(cluster.CacheNodeType),
		}
		if cluster.CacheParameterGroup != nil {
			ecLive["parameter_group"] = stringValue(cluster.CacheParameterGroup.CacheParameterGroupName)
		}
		if len(cluster.SecurityGroups) > 0 {
			sgIDs := make([]string, 0, len(cluster.SecurityGroups))
			for _, sg := range cluster.SecurityGroups {
				if sg.SecurityGroupId != nil {
					sgIDs = append(sgIDs, *sg.SecurityGroupId)
				}
			}
			ecLive["security_groups"] = sgIDs
		}
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: ecLive}, nil
	case "lambda":
		name := rec.ProviderID
		if name == "" {
			name = trimResourceName(identifierFor(rec.NodeName), 64)
		}
		lc := lambda.NewFromConfig(cfg)
		lOut, err := lc.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: awsString(name)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("lambda get function: %w", err)
		}
		lambdaLive := map[string]interface{}{"service": "lambda"}
		if lOut.Configuration != nil {
			lambdaLive["runtime"] = string(lOut.Configuration.Runtime)
			if lOut.Configuration.MemorySize != nil {
				lambdaLive["memory_size"] = *lOut.Configuration.MemorySize
			}
			if lOut.Configuration.Timeout != nil {
				lambdaLive["timeout"] = *lOut.Configuration.Timeout
			}
			lambdaLive["handler"] = stringValue(lOut.Configuration.Handler)
			lambdaLive["last_modified"] = stringValue(lOut.Configuration.LastModified)
			if lOut.Configuration.Environment != nil && lOut.Configuration.Environment.Variables != nil {
				envVars := make(map[string]interface{}, len(lOut.Configuration.Environment.Variables))
				for k, v := range lOut.Configuration.Environment.Variables {
					envVars[k] = v
				}
				lambdaLive["environment"] = security.ScrubMap(envVars)
			}
		}
		return &ObserveResult{Exists: true, ProviderID: name, LiveState: lambdaLive}, nil
	case "cloudfront":
		id := rec.ProviderID
		if id == "" {
			return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
		}
		c := cloudfront.NewFromConfig(cfg)
		out, err := c.GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: awsString(id)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("cloudfront get distribution: %w", err)
		}
		domain := ""
		enabled := false
		status := ""
		if out.Distribution != nil {
			if out.Distribution.DomainName != nil {
				domain = *out.Distribution.DomainName
			}
			if out.Distribution.DistributionConfig != nil && out.Distribution.DistributionConfig.Enabled != nil {
				enabled = *out.Distribution.DistributionConfig.Enabled
			}
			if out.Distribution.Status != nil {
				status = *out.Distribution.Status
			}
		}
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: map[string]interface{}{"service": "cloudfront", "distribution_id": id, "domain_name": domain, "status": status, "enabled": enabled}}, nil
	case "route53":
		id := rec.ProviderID
		if id == "" {
			return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
		}
		c := route53.NewFromConfig(cfg)
		out, err := c.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: awsString(id)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("route53 get hosted zone: %w", err)
		}
		domain := ""
		if out.HostedZone != nil && out.HostedZone.Name != nil {
			domain = *out.HostedZone.Name
		}
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: map[string]interface{}{"service": "route53", "hosted_zone_id": id, "domain": domain}}, nil
	case "cloudwatch":
		name := rec.ProviderID
		if name == "" {
			name = identifierFor(rec.NodeName)
		}
		c := cloudwatch.NewFromConfig(cfg)
		out, err := c.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{AlarmNames: []string{name}})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("cloudwatch describe alarms: %w", err)
		}
		if len(out.MetricAlarms) == 0 {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		alarm := out.MetricAlarms[0]
		return &ObserveResult{Exists: true, ProviderID: name, LiveState: map[string]interface{}{"service": "cloudwatch", "alarm_name": name, "state": string(alarm.StateValue), "metric_name": stringValue(alarm.MetricName), "namespace": stringValue(alarm.Namespace)}}, nil
	case "eks":
		name := rec.ProviderID
		if name == "" {
			name = identifierFor(rec.NodeName)
		}
		c := eks.NewFromConfig(cfg)
		out, err := c.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: awsString(name)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("eks describe cluster: %w", err)
		}
		status := ""
		endpoint := ""
		if out.Cluster != nil {
			status = string(out.Cluster.Status)
			endpoint = stringValue(out.Cluster.Endpoint)
		}
		return &ObserveResult{Exists: true, ProviderID: name, LiveState: map[string]interface{}{"service": "eks", "cluster_name": name, "status": status, "endpoint": endpoint}}, nil
	case "eventbridge":
		name := rec.ProviderID
		if name == "" {
			name = identifierFor(rec.NodeName)
		}
		c := eventbridge.NewFromConfig(cfg)
		out, err := c.DescribeRule(ctx, &eventbridge.DescribeRuleInput{Name: awsString(name)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("eventbridge describe rule: %w", err)
		}
		live := map[string]interface{}{"service": "eventbridge", "rule_name": name}
		if out.EventPattern != nil {
			live["event_pattern"] = *out.EventPattern
		}
		if out.ScheduleExpression != nil {
			live["schedule_expression"] = *out.ScheduleExpression
		}
		if out.State != "" {
			live["state"] = string(out.State)
		}
		return &ObserveResult{Exists: true, ProviderID: name, LiveState: live}, nil
	case "cognito":
		id := rec.ProviderID
		if id == "" {
			return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
		}
		c := cognitoidentityprovider.NewFromConfig(cfg)
		out, err := c.DescribeUserPool(ctx, &cognitoidentityprovider.DescribeUserPoolInput{UserPoolId: awsString(id)})
		if err != nil {
			if isNotFound(err) {
				return &ObserveResult{Exists: false, ProviderID: id, LiveState: map[string]interface{}{}}, nil
			}
			return nil, fmt.Errorf("cognito describe user pool: %w", err)
		}
		name := ""
		status := ""
		if out.UserPool != nil {
			name = stringValue(out.UserPool.Name)
			status = string(out.UserPool.Status)
		}
		return &ObserveResult{Exists: true, ProviderID: id, LiveState: map[string]interface{}{"service": "cognito", "user_pool_id": id, "name": name, "status": status}}, nil
	default:
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
}

func detectAWSTarget(req ApplyRequest) string {
	nodeType := strings.ToUpper(req.Action.NodeType)
	engine := strings.ToLower(intent(req.Intent, "engine", "type", "runtime", "service", "topology", "resource"))
	expose := strings.ToLower(intent(req.Intent, "expose"))
	if nodeType == "STORE" {
		switch {
		case strings.Contains(engine, "aurora"):
			return "rds_aurora_serverless"
		case strings.Contains(engine, "postgres"), strings.Contains(engine, "mysql"):
			return "rds"
		case strings.Contains(engine, "redis"):
			return "elasticache"
		case strings.Contains(engine, "sqs"):
			return "sqs"
		case strings.Contains(engine, "sns"):
			return "sns"
		case strings.Contains(engine, "s3"), strings.Contains(engine, "bucket"):
			return "s3"
		case strings.Contains(engine, "secret"):
			return "secrets_manager"
		}
	}
	if nodeType == "NETWORK" {
		switch {
		case strings.Contains(engine, "vpc"):
			return "vpc"
		case strings.Contains(engine, "subnet"):
			return "subnet"
		case strings.Contains(engine, "security_group") || strings.Contains(engine, "sg"):
			return "security_group"
		case strings.Contains(engine, "alb"):
			return "alb"
		case strings.Contains(engine, "api_gateway"), strings.Contains(engine, "apigateway"):
			return "api_gateway"
		case strings.Contains(engine, "cloudfront"):
			return "cloudfront"
		case strings.Contains(engine, "route53") || strings.Contains(engine, "dns"):
			return "route53"
		case strings.Contains(engine, "cloudwatch"):
			return "cloudwatch"
		}
	}
	if nodeType == "SERVICE" {
		switch {
		case strings.Contains(engine, "lambda"):
			return "lambda"
		case strings.Contains(engine, "container"):
			if strings.Contains(expose, "api") {
				return "api_gateway"
			}
			return "ecs"
		case strings.Contains(engine, "eks"):
			return "eks"
		case strings.Contains(engine, "cognito"):
			return "cognito"
		case strings.Contains(engine, "ec2"):
			return "ec2"
		}
	}
	if nodeType == "COMPUTE" {
		switch {
		case strings.Contains(engine, "lambda"):
			return "lambda"
		case strings.Contains(engine, "eventbridge"):
			return "eventbridge"
		case strings.Contains(engine, "cloudwatch"):
			return "cloudwatch"
		case strings.Contains(engine, "cognito"):
			return "cognito"
		case strings.Contains(engine, "ec2"):
			return "ec2"
		}
	}
	// Fallback heuristics from fields — sorted for deterministic results.
	// NOTE: This is intentionally greedy and may produce false positives
	// (e.g., a description containing "s3" would match target "s3").
	// The primary detection above should handle all well-formed intents.
	intentKeys := make([]string, 0, len(req.Intent))
	for k := range req.Intent {
		intentKeys = append(intentKeys, k)
	}
	sort.Strings(intentKeys)
	targets := make([]string, 0, len(AWSSupportMatrix))
	for t := range AWSSupportMatrix {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, k := range intentKeys {
		s := strings.ToLower(fmt.Sprint(req.Intent[k]))
		for _, target := range targets {
			if strings.Contains(s, strings.ReplaceAll(target, "_", "")) || strings.Contains(s, target) {
				return target
			}
		}
	}
	return "generic"
}

func detectRecordTarget(rec *state.ResourceRecord) string {
	if rec == nil {
		return "generic"
	}
	if svc := strings.ToLower(fmt.Sprint(rec.LiveState["service"])); svc != "" {
		switch svc {
		case "rds":
			return "rds"
		case "s3":
			return "s3"
		case "sqs":
			return "sqs"
		case "sns":
			return "sns"
		case "secretsmanager":
			return "secrets_manager"
		case "iam":
			return "iam"
		case "lambda":
			return "lambda"
		case "elasticache":
			return "elasticache"
		case "cloudfront":
			return "cloudfront"
		case "route53":
			return "route53"
		case "cloudwatch":
			return "cloudwatch"
		case "eks":
			return "eks"
		case "eventbridge":
			return "eventbridge"
		case "cognito":
			return "cognito"
		case "ec2":
			res := strings.ToLower(fmt.Sprint(rec.LiveState["resource"]))
			switch res {
			case "vpc", "subnet", "security_group", "ec2":
				return res
			}
		}
	}
	if rec.NodeType == "STORE" {
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		typ := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.type"]))
		switch {
		case strings.Contains(eng, "postgres"), strings.Contains(eng, "mysql"):
			return "rds"
		case strings.Contains(eng, "aurora"):
			return "rds_aurora_serverless"
		case strings.Contains(eng, "redis"):
			return "elasticache"
		case strings.Contains(eng, "s3"), strings.Contains(typ, "s3"):
			return "s3"
		case strings.Contains(eng, "secret"):
			return "secrets_manager"
		case strings.Contains(eng, "sqs"):
			return "sqs"
		case strings.Contains(eng, "sns"):
			return "sns"
		}
	}
	if rec.NodeType == "NETWORK" {
		top := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.topology"]))
		switch {
		case strings.Contains(top, "vpc"):
			return "vpc"
		case strings.Contains(top, "subnet"):
			return "subnet"
		case strings.Contains(top, "security_group"), strings.Contains(top, "sg"):
			return "security_group"
		case strings.Contains(top, "cloudfront"):
			return "cloudfront"
		case strings.Contains(top, "route53"), strings.Contains(top, "dns"):
			return "route53"
		}
	}
	if rec.NodeType == "SERVICE" {
		runtime := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.runtime"]))
		if strings.Contains(runtime, "lambda") {
			return "lambda"
		}
		if strings.Contains(runtime, "sqs") {
			return "sqs"
		}
		if strings.Contains(runtime, "sns") {
			return "sns"
		}
		if strings.Contains(runtime, "iam") {
			return "iam"
		}
		if strings.Contains(runtime, "eks") {
			return "eks"
		}
		if strings.Contains(runtime, "cognito") {
			return "cognito"
		}
		if strings.Contains(runtime, "ec2") {
			return "ec2"
		}
	}
	if rec.NodeType == "COMPUTE" {
		runtime := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.runtime"]))
		if strings.Contains(runtime, "eventbridge") {
			return "eventbridge"
		}
		if strings.Contains(runtime, "cloudwatch") {
			return "cloudwatch"
		}
	}
	return "generic"
}

func simulatedApply(req ApplyRequest, target string) *ApplyResult {
	providerID := req.RecordProviderID()
	if providerID == "" {
		providerID = fmt.Sprintf("simulated:%s:%s", strings.ToLower(req.Provider), identifierFor(req.Action.NodeName))
	}
	live := map[string]interface{}{
		"provider":    strings.ToLower(req.Provider),
		"provider_id": providerID,
		"target":      target,
		"operation":   req.Action.Operation,
		"simulated":   true,
	}
	for k, v := range req.Intent {
		if security.IsSensitiveKey(k) {
			continue
		}
		live[k] = v
	}
	return &ApplyResult{ProviderID: providerID, LiveState: live}
}

func (r ApplyRequest) RecordProviderID() string {
	if r.Record == nil {
		return ""
	}
	return r.Record.ProviderID
}

func identifierFor(name string) string {
	id := strings.ToLower(name)
	id = strings.ReplaceAll(id, "_", "-")
	id = strings.ReplaceAll(id, ".", "-")
	id = strings.ReplaceAll(id, " ", "-")
	if len(id) > 50 {
		id = id[:50]
	}
	return "beecon-" + id
}

func parseStorageGiB(disk string) int32 {
	disk = strings.TrimSpace(strings.ToLower(disk))
	if disk == "" {
		return 0
	}
	disk = strings.TrimSuffix(disk, "gb")
	disk = strings.TrimSpace(disk)
	n, err := strconv.Atoi(disk)
	if err != nil || n <= 0 {
		return 0
	}
	return int32(n)
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func trimResourceName(v string, max int) string {
	if len(v) <= max {
		return v
	}
	return v[:max]
}

func intent(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m["intent."+k]; ok {
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return ""
}

func stringListFromIntent(m map[string]interface{}, key string) []string {
	raw := intent(m, key)
	if raw == "" {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(raw, "]"), "["))
	}
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		item := strings.TrimSpace(strings.Trim(p, `"`))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func rdsCredentials(intentMap map[string]interface{}) (string, string, error) {
	user := intent(intentMap, "username")
	pass := intent(intentMap, "password")
	if user == "" || pass == "" {
		return "", "", fmt.Errorf("rds create requires intent.username and intent.password")
	}
	return user, pass, nil
}

func runtimeFromString(v string) lambdatypes.Runtime {
	if strings.TrimSpace(v) == "" {
		return lambdatypes.Runtime("provided.al2")
	}
	return lambdatypes.Runtime(v)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFoundException", "ResourceNotFoundException", "NoSuchEntity",
			"NoSuchBucket", "DBInstanceNotFoundFault", "CacheClusterNotFound",
			"ClusterNotFoundException",
			// EC2
			"InvalidGroup.NotFound", "InvalidInstanceID.NotFound",
			"InvalidSubnetID.NotFound", "InvalidVpcID.NotFound",
			// CloudFront / Route53
			"NoSuchDistribution", "NoSuchHostedZone",
			// ElastiCache / SQS / RDS
			"ReplicationGroupNotFoundFault", "QueueDoesNotExist",
			"AWS.SimpleQueueService.NonExistentQueue",
			"DBParameterGroupNotFoundFault",
			// Cognito
			"UserPoolNotFoundException":
			return true
		}
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") || strings.Contains(s, "404")
}

func cloudFrontDistributionConfigFromIntent(intentMap map[string]interface{}, callerRefBase, callerRefSuffix string) (*cloudfronttypes.DistributionConfig, error) {
	raw := strings.TrimSpace(intent(intentMap, "distribution_config_json"))
	if raw == "" {
		return nil, fmt.Errorf("cloudfront requires intent.distribution_config_json")
	}
	cfg := &cloudfronttypes.DistributionConfig{}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return nil, fmt.Errorf("cloudfront distribution config json: %w", err)
	}
	if cfg.CallerReference == nil || strings.TrimSpace(*cfg.CallerReference) == "" {
		ref := callerRefBase
		if callerRefSuffix != "" {
			ref = callerRefBase + "-" + callerRefSuffix
		}
		cfg.CallerReference = awsString(ref)
	}
	return cfg, nil
}

func cloudWatchStatisticFromString(v string) cloudwatchtypes.Statistic {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "sum":
		return cloudwatchtypes.StatisticSum
	case "minimum", "min":
		return cloudwatchtypes.StatisticMinimum
	case "maximum", "max":
		return cloudwatchtypes.StatisticMaximum
	case "samplecount", "sample_count":
		return cloudwatchtypes.StatisticSampleCount
	default:
		return cloudwatchtypes.StatisticAverage
	}
}

func awsString(v string) *string { return &v }
func awsBool(v bool) *bool       { return &v }
func awsInt32(v int32) *int32    { return &v }
func awsFloat64(v float64) *float64 {
	return &v
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func intValue(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// --- C0: Helpers + Validation Framework ---

// SGRule represents a parsed security group rule in compact format.
type SGRule struct {
	Protocol string // tcp, udp, icmp, -1
	FromPort int32
	ToPort   int32
	CIDR     string
}

// parseIntIntent reads a string-valued intent key and converts to int32 with fallback.
func parseIntIntent(m map[string]interface{}, key string, fallback int32) int32 {
	raw := strings.TrimSpace(intent(m, key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return int32(n)
}

// parseBoolIntent reads a string-valued intent key and converts to bool.
// Accepts "true"/"1" as true, everything else as false.
func parseBoolIntent(m map[string]interface{}, key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(intent(m, key)))
	if raw == "" {
		return fallback
	}
	return raw == "true" || raw == "1"
}

// envFromIntent extracts env.* prefixed keys into a plain map.
// e.g. intent key "env.DB_HOST" → map entry "DB_HOST".
func envFromIntent(m map[string]interface{}) map[string]string {
	out := make(map[string]string)
	for k, v := range m {
		if strings.HasPrefix(k, "intent.env.") {
			envKey := strings.TrimPrefix(k, "intent.env.")
			if envKey != "" {
				out[envKey] = strings.TrimSpace(fmt.Sprint(v))
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validServicePrincipal validates that a service principal matches the expected AWS format.
var validServicePrincipal = regexp.MustCompile(`^[a-z0-9\-]+(\.[a-z0-9\-]+)*\.amazonaws\.com$`)

// trustPolicyForService generates an assume-role trust policy JSON document
// for a given AWS service principal. Returns an error if the service doesn't
// match the expected format (prevents JSON injection).
func trustPolicyForService(service string) (string, error) {
	if !validServicePrincipal.MatchString(service) {
		return "", fmt.Errorf("invalid service principal %q: must match <service>.amazonaws.com", service)
	}
	return fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"%s"},"Action":"sts:AssumeRole"}]}`, service), nil
}

// parseSecurityGroupRules parses the compact rule format: "tcp:443:10.0.0.0/16"
// Supports port ranges: "tcp:8000-8080:10.0.0.0/16"
// ICMP uses port -1: "icmp:-1:0.0.0.0/0"
// All traffic: "-1:0:0.0.0.0/0"
func parseSecurityGroupRules(raw string) ([]SGRule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	rules := make([]SGRule, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		segments := strings.SplitN(p, ":", 3)
		if len(segments) != 3 {
			return nil, fmt.Errorf("invalid security group rule %q: expected protocol:port:cidr", p)
		}
		protocol := strings.ToLower(strings.TrimSpace(segments[0]))
		switch protocol {
		case "tcp", "udp", "icmp", "-1":
		default:
			return nil, fmt.Errorf("invalid protocol %q in rule %q: must be tcp, udp, icmp, or -1", protocol, p)
		}
		portStr := strings.TrimSpace(segments[1])
		cidr := strings.TrimSpace(segments[2])
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return nil, fmt.Errorf("invalid CIDR %q in rule %q: %w", cidr, p, err)
		}
		var fromPort, toPort int32
		// Check for port range (e.g., "8000-8080") but not negative numbers (e.g., "-1").
		// A range has a dash that is not at position 0.
		if idx := strings.Index(portStr, "-"); idx > 0 {
			from, err := strconv.Atoi(strings.TrimSpace(portStr[:idx]))
			if err != nil {
				return nil, fmt.Errorf("invalid from port in rule %q: %w", p, err)
			}
			to, err := strconv.Atoi(strings.TrimSpace(portStr[idx+1:]))
			if err != nil {
				return nil, fmt.Errorf("invalid to port in rule %q: %w", p, err)
			}
			fromPort, toPort = int32(from), int32(to)
		} else {
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return nil, fmt.Errorf("invalid port in rule %q: %w", p, err)
			}
			fromPort, toPort = int32(port), int32(port)
		}
		// Validate port ranges per protocol
		switch protocol {
		case "tcp", "udp":
			if fromPort < 0 || toPort > 65535 || fromPort > toPort {
				return nil, fmt.Errorf("invalid port range %d-%d in rule %q: tcp/udp ports must be 0-65535 with from <= to", fromPort, toPort, p)
			}
		case "icmp":
			// ICMP uses -1 for type/code meaning "all"
		case "-1":
			// All traffic — ports are ignored by AWS
		}
		rules = append(rules, SGRule{Protocol: protocol, FromPort: fromPort, ToPort: toPort, CIDR: cidr})
	}
	return rules, nil
}

// serializeSGRules converts SDK IpPermission structs back to compact string format.
func serializeSGRules(perms []ec2types.IpPermission) string {
	if len(perms) == 0 {
		return "[]"
	}
	parts := make([]string, 0)
	for _, p := range perms {
		proto := "-1"
		if p.IpProtocol != nil {
			proto = *p.IpProtocol
		}
		var from, to int32
		if p.FromPort != nil {
			from = *p.FromPort
		}
		if p.ToPort != nil {
			to = *p.ToPort
		}
		for _, r := range p.IpRanges {
			cidr := ""
			if r.CidrIp != nil {
				cidr = *r.CidrIp
			}
			portStr := fmt.Sprintf("%d", from)
			if from != to {
				portStr = fmt.Sprintf("%d-%d", from, to)
			}
			parts = append(parts, fmt.Sprintf("%s:%s:%s", proto, portStr, cidr))
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// buildNewPermsFromIntent parses intent rules and returns SDK IpPermissions.
// Returns nil, nil if the intent key is empty.
func buildNewPermsFromIntent(intentMap map[string]interface{}, key string) ([]ec2types.IpPermission, error) {
	raw := intent(intentMap, key)
	if raw == "" {
		return nil, nil
	}
	rules, err := parseSecurityGroupRules(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s rules: %w", key, err)
	}
	if len(rules) == 0 {
		return nil, nil
	}
	return sgRulesToIPPermissions(rules), nil
}

// diffIPPermissions returns permissions in old that are not present in new.
// Comparison is by serialized compact format for simplicity.
func diffIPPermissions(old, new []ec2types.IpPermission) []ec2types.IpPermission {
	if len(old) == 0 {
		return nil
	}
	newSet := make(map[string]bool, len(new))
	for _, p := range new {
		key := ipPermissionKey(p)
		newSet[key] = true
	}
	var stale []ec2types.IpPermission
	for _, p := range old {
		key := ipPermissionKey(p)
		if !newSet[key] {
			stale = append(stale, p)
		}
	}
	return stale
}

// ipPermissionKey creates a string key for an IpPermission for set comparison.
func ipPermissionKey(p ec2types.IpPermission) string {
	proto := "-1"
	if p.IpProtocol != nil {
		proto = *p.IpProtocol
	}
	var from, to int32
	if p.FromPort != nil {
		from = *p.FromPort
	}
	if p.ToPort != nil {
		to = *p.ToPort
	}
	var cidrs []string
	for _, r := range p.IpRanges {
		if r.CidrIp != nil {
			cidrs = append(cidrs, *r.CidrIp)
		}
	}
	sort.Strings(cidrs)
	return fmt.Sprintf("%s:%d:%d:%s", proto, from, to, strings.Join(cidrs, ","))
}

// sgRulesToIPPermissions converts parsed SGRules to SDK IpPermission structs.
func sgRulesToIPPermissions(rules []SGRule) []ec2types.IpPermission {
	perms := make([]ec2types.IpPermission, 0, len(rules))
	for _, r := range rules {
		perms = append(perms, ec2types.IpPermission{
			IpProtocol: awsString(r.Protocol),
			FromPort:   awsInt32(r.FromPort),
			ToPort:     awsInt32(r.ToPort),
			IpRanges:   []ec2types.IpRange{{CidrIp: awsString(r.CIDR)}},
		})
	}
	return perms
}

// applySGRules parses and applies ingress/egress rules to a security group.
func applySGRules(ctx context.Context, c *ec2.Client, sgID string, intentMap map[string]interface{}) error {
	if raw := intent(intentMap, "ingress"); raw != "" {
		rules, err := parseSecurityGroupRules(raw)
		if err != nil {
			return fmt.Errorf("ec2 parse ingress rules: %w", err)
		}
		if len(rules) > 0 {
			if _, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
				GroupId:       awsString(sgID),
				IpPermissions: sgRulesToIPPermissions(rules),
			}); err != nil {
				return fmt.Errorf("ec2 authorize ingress: %w", err)
			}
		}
	}
	if raw := intent(intentMap, "egress"); raw != "" {
		rules, err := parseSecurityGroupRules(raw)
		if err != nil {
			return fmt.Errorf("ec2 parse egress rules: %w", err)
		}
		if len(rules) > 0 {
			if _, err := c.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
				GroupId:       awsString(sgID),
				IpPermissions: sgRulesToIPPermissions(rules),
			}); err != nil {
				return fmt.Errorf("ec2 authorize egress: %w", err)
			}
		}
	}
	return nil
}

// validateAWSInput performs input validation per AWS target type.
func validateAWSInput(target string, intentMap map[string]interface{}) error {
	switch target {
	case "lambda":
		mem := parseIntIntent(intentMap, "memory", 128)
		if mem < 128 || mem > 10240 {
			return fmt.Errorf("lambda memory must be 128-10240 MB, got %d", mem)
		}
		timeout := parseIntIntent(intentMap, "timeout", 30)
		if timeout < 1 || timeout > 900 {
			return fmt.Errorf("lambda timeout must be 1-900 seconds, got %d", timeout)
		}
	case "rds":
		if iops := intent(intentMap, "iops"); iops != "" {
			storageType := defaultString(intent(intentMap, "storage_type"), "gp3")
			switch storageType {
			case "io1", "io2", "gp3":
			default:
				return fmt.Errorf("iops requires storage_type io1, io2, or gp3, got %q", storageType)
			}
		}
	case "iam":
		for _, p := range stringListFromIntent(intentMap, "managed_policies") {
			if !strings.HasPrefix(p, "arn:") {
				return fmt.Errorf("managed_policies entry %q must start with arn:", p)
			}
		}
		if svc := intent(intentMap, "trust_service"); svc != "" {
			if !validServicePrincipal.MatchString(svc) {
				return fmt.Errorf("invalid trust_service %q: must match <service>.amazonaws.com", svc)
			}
		}
	}
	return nil
}

// detectTrustService auto-detects the AWS service principal for IAM trust policies
// based on the runtime hint in the intent map.
func detectTrustService(intentMap map[string]interface{}) string {
	runtime := strings.ToLower(intent(intentMap, "runtime"))
	switch {
	case strings.Contains(runtime, "lambda"):
		return "lambda.amazonaws.com"
	case strings.Contains(runtime, "ec2"):
		return "ec2.amazonaws.com"
	case strings.Contains(runtime, "eks"):
		return "eks.amazonaws.com"
	default:
		return "ecs-tasks.amazonaws.com"
	}
}
