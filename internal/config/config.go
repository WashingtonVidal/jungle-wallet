package config

import (
	"fmt"
	"os"
)

type Config struct {
	InternalClientID string
	DatabaseURL      string
	HTTPAddress      string
	OIDCIssuerURL    string

	AWSRegion         string
	SQSEndpoint       string
	SQSQueueURL       string
	SQSOutboxQueueURL string
}

func New() (*Config, error) {
	internalClientID := os.Getenv("INTERNAL_CLIENT_ID")
	if internalClientID == "" {
		internalClientID = "wallet-internal"
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	httpAddress := os.Getenv("HTTP_ADDRESS")
	if httpAddress == "" {
		httpAddress = ":8080"
	}

	oidcIssuerURL := os.Getenv("OIDC_ISSUER_URL")
	if oidcIssuerURL == "" {
		return nil, fmt.Errorf("OIDC_ISSUER_URL is required")
	}

	awsRegion := os.Getenv("AWS_REGION")
	if awsRegion == "" {
		awsRegion = "us-east-1"
	}

	sqsEndpoint := os.Getenv("SQS_ENDPOINT")
	if sqsEndpoint == "" {
		sqsEndpoint = "http://localhost:4566"
	}

	sqsQueueURL := os.Getenv("SQS_QUEUE_URL")
	if sqsQueueURL == "" {
		sqsQueueURL = "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions.fifo"
	}

	sqsOutboxQueueURL := os.Getenv("SQS_OUTBOX_QUEUE_URL")
	if sqsOutboxQueueURL == "" {
		sqsOutboxQueueURL = "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-events.fifo"
	}

	return &Config{
		InternalClientID:  internalClientID,
		DatabaseURL:       databaseURL,
		HTTPAddress:       httpAddress,
		OIDCIssuerURL:     oidcIssuerURL,
		AWSRegion:         awsRegion,
		SQSEndpoint:       sqsEndpoint,
		SQSQueueURL:       sqsQueueURL,
		SQSOutboxQueueURL: sqsOutboxQueueURL,
	}, nil
}
