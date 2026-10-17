// Package auth verifies OIDC bearer tokens.
package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Claims struct {
	Subject     string
	Email       string
	DisplayName string
	// AuthTime is when the user last actually logged in (OIDC auth_time). Zero if the provider
	// did not send it. A refreshed token keeps the original value, which is what makes it usable
	// as a "fresh login" proof.
	AuthTime time.Time
}

type Verifier interface {
	Verify(ctx context.Context, rawToken string) (Claims, error)
}

type OIDC struct{ v *oidc.IDTokenVerifier }

// NewOIDC discovers the provider and checks issuer, audience, signature and expiry.
func NewOIDC(ctx context.Context, issuer, audience string) (*OIDC, error) {
	return NewOIDCVia(ctx, issuer, "", audience)
}

// NewOIDCVia is NewOIDC for setups where the server cannot reach the issuer's public address
// (typically a container next to the identity provider): the provider's metadata is fetched from
// discoveryURL (an internal address), while tokens must still carry the public issuer. An empty
// discoveryURL means "use the issuer itself".
func NewOIDCVia(ctx context.Context, issuer, discoveryURL, audience string) (*OIDC, error) {
	src := issuer
	if discoveryURL != "" {
		src = discoveryURL
		ctx = oidc.InsecureIssuerURLContext(ctx, issuer)
	}
	p, err := oidc.NewProvider(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("auth: discover provider: %w", err)
	}
	return &OIDC{v: p.Verifier(&oidc.Config{ClientID: audience})}, nil
}

func (o *OIDC) Verify(ctx context.Context, raw string) (Claims, error) {
	t, err := o.v.Verify(ctx, raw)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: verify: %w", err)
	}
	var c struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		// Providers send auth_time as a JSON number (seconds since epoch).
		AuthTime float64 `json:"auth_time"`
	}
	if err := t.Claims(&c); err != nil {
		return Claims{}, fmt.Errorf("auth: claims: %w", err)
	}
	cl := Claims{Subject: t.Subject, Email: c.Email, DisplayName: c.Name}
	if c.AuthTime > 0 {
		cl.AuthTime = time.Unix(int64(c.AuthTime), 0)
	}
	return cl, nil
}
