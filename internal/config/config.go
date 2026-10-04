package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL   string
	HTTPAddress   string
	OIDCIssuerURL string
}

func New() (*Config, error) {
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

	return &Config{
		DatabaseURL:   databaseURL,
		HTTPAddress:   httpAddress,
		OIDCIssuerURL: oidcIssuerURL,
	}, nil
}
