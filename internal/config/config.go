package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL string
	HTTPAddress string
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

	return &Config{
		DatabaseURL: databaseURL,
		HTTPAddress: httpAddress,
	}, nil
}
