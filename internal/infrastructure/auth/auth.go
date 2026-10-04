package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"jungle-wallet/internal/config"
)

type Claims struct {
	AuthorizedParty string `json:"azp"`
	ClientID        string `json:"client_id"`
}

type Authenticator struct {
	verifier *oidc.IDTokenVerifier
}

func NewAuthenticator(ctx context.Context, issuerURL string) (*Authenticator, error) {
	if issuerURL == "" {
		return nil, fmt.Errorf("OIDC issuer URL is required")
	}

	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("create OIDC provider: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{
		SkipClientIDCheck: true,
	})

	return &Authenticator{
		verifier: verifier,
	}, nil
}

func (a *Authenticator) Authenticate(
	ctx context.Context,
	header string,
) (*Claims, error) {
	if a == nil || a.verifier == nil {
		return nil, fmt.Errorf("authenticator is not initialized")
	}

	const prefix = "Bearer "

	if !strings.HasPrefix(header, prefix) {
		return nil, fmt.Errorf("missing bearer token")
	}

	rawToken := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if rawToken == "" {
		return nil, fmt.Errorf("missing bearer token")
	}

	token, err := a.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("verify access token: %w", err)
	}

	var claims Claims
	if err := token.Claims(&claims); err != nil {
		return nil, fmt.Errorf("decode access token claims: %w", err)
	}

	if claims.ClientID == "" {
		claims.ClientID = claims.AuthorizedParty
	}

	if claims.ClientID == "" {
		return nil, fmt.Errorf("token does not identify a client")
	}

	return &claims, nil
}

func BearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}

	return r.Header.Get("Authorization")
}

func NewFromConfig(cfg *config.Config) (*Authenticator, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	return NewAuthenticator(context.Background(), cfg.OIDCIssuerURL)
}

type contextKey string

const claimsContextKey contextKey = "auth-claims"

func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey, claims)
}

func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(*Claims)
	return claims, ok && claims != nil
}
