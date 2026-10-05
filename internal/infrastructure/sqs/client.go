package sqs

import (
	"context"
	"fmt"
	"net/url"
	"os"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"jungle-wallet/internal/config"
)

func NewClient(
	ctx context.Context,
	cfg *config.Config,
) (*awssqs.Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	// Real deployments use the SDK credential chain (environment, profiles or roles).
	// Dummy credentials are restricted to the local emulator when none were supplied.
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.AWSRegion)}
	endpoint, _ := url.Parse(cfg.SQSEndpoint)
	if endpoint != nil && (endpoint.Hostname() == "localhost" || endpoint.Hostname() == "127.0.0.1") && os.Getenv("AWS_ACCESS_KEY_ID") == "" && os.Getenv("AWS_PROFILE") == "" {
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := awssqs.NewFromConfig(
		awsCfg,
		func(options *awssqs.Options) {
			if cfg.SQSEndpoint != "" {
				options.BaseEndpoint = &cfg.SQSEndpoint
			}
		},
	)

	return client, nil
}
