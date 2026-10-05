package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Use Keycloak's genuine signed access token and advance only the verifier clock.
// No fabricated signature, realm changes, or real-time expiry sleep is needed.
func TestKeycloakExpiredAccessTokenIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := os.ReadFile("../../../deploy/keycloak/realm-export.json")
	if err != nil {
		t.Fatal(err)
	}
	var realm struct {
		Clients []struct {
			ID     string `json:"clientId"`
			Secret string `json:"secret"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(raw, &realm); err != nil {
		t.Fatal(err)
	}
	secret := ""
	for _, c := range realm.Clients {
		if c.ID == "provider-a" {
			secret = c.Secret
		}
	}
	issuer := "http://localhost:8081/realms/jungle-wallet"
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {"provider-a"}, "client_secret": {secret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("token endpoint status %d", response.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewAuthenticator(ctx, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.Authenticate(ctx, "Bearer "+body.AccessToken); err != nil {
		t.Fatalf("valid signed token rejected: %v", err)
	}
	parts := strings.Split(body.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatal("invalid token format")
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Expires int64 `json:"exp"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil || claims.Expires == 0 {
		t.Fatal("missing expiration")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		t.Fatal(err)
	}
	authenticator.verifier = provider.Verifier(&oidc.Config{SkipClientIDCheck: true, Now: func() time.Time { return time.Unix(claims.Expires+1, 0) }})
	if _, err := authenticator.Authenticate(ctx, "Bearer "+body.AccessToken); err == nil {
		t.Fatal("expired signed token accepted")
	}
}
