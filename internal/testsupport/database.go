package testsupport

import (
	"net/url"
	"os"
	"strings"
)

// DatabaseURL isolates integration fixtures from the application's data.
func DatabaseURL() string {
	value := os.Getenv("JUNGLE_TEST_DATABASE_URL")
	if value == "" {
		value = "postgres://jungle:jungle@localhost:5432/jungle_wallet_test?sslmode=disable"
	}
	parsed, err := url.Parse(value)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		panic("JUNGLE_TEST_DATABASE_URL must identify a dedicated database ending in _test")
	}
	return value
}
