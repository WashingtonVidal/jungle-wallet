package sqs

import (
	"context"
	"fmt"

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

	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(cfg.AWSRegion),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				"test",
				"test",
				"",
			),
		),
	)
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
