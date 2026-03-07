package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type ConnectionResult struct {
	Provider string
	Region   string
	Identity string
}

func Connect(ctx context.Context, provider, region string) (*ConnectionResult, error) {
	switch strings.ToLower(provider) {
	case "aws":
		return connectAWS(ctx, region)
	case "gcp":
		return connectGCP(ctx, region)
	case "azure":
		return connectAzure(ctx, region)
	default:
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}
}

func connectAWS(ctx context.Context, region string) (*ConnectionResult, error) {
	if region == "" {
		region = "us-east-1"
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(cctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	client := sts.NewFromConfig(cfg)
	out, err := client.GetCallerIdentity(cctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("aws sts get-caller-identity: %w", err)
	}
	identity := "unknown"
	if out.Arn != nil {
		identity = *out.Arn
	}
	return &ConnectionResult{Provider: "aws", Region: region, Identity: identity}, nil
}

func connectGCP(ctx context.Context, region string) (*ConnectionResult, error) {
	creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if creds == "" {
		return nil, fmt.Errorf("gcp credentials not configured: set GOOGLE_APPLICATION_CREDENTIALS")
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := storage.NewClient(cctx)
	if err != nil {
		return nil, fmt.Errorf("gcp storage client init: %w", err)
	}
	_ = client.Close()
	return &ConnectionResult{Provider: "gcp", Region: region, Identity: creds}, nil
}

func connectAzure(ctx context.Context, region string) (*ConnectionResult, error) {
	clientID := os.Getenv("AZURE_CLIENT_ID")
	tenantID := os.Getenv("AZURE_TENANT_ID")
	secret := os.Getenv("AZURE_CLIENT_SECRET")
	if clientID == "" || tenantID == "" || secret == "" {
		return nil, fmt.Errorf("azure credentials not configured: set AZURE_CLIENT_ID, AZURE_TENANT_ID, AZURE_CLIENT_SECRET")
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential init: %w", err)
	}
	_ = cctx
	return &ConnectionResult{Provider: "azure", Region: region, Identity: clientID}, nil
}
