// Package identity removes users at the identity provider (the OIDC server that logs them in).
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Provider deletes a login identity. Deleting one that is already gone must succeed.
type Provider interface {
	DeleteUser(ctx context.Context, subject string) error
}

// Keycloak deletes users through the Keycloak admin REST API, authenticating as a service account
// (client-credentials grant) that has only the realm-management "manage-users" role.
type Keycloak struct {
	BaseURL      string // e.g. https://sso.example.com (no trailing path)
	Realm        string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client // nil means a client with a 10s timeout

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func (k *Keycloak) client() *http.Client {
	if k.HTTP != nil {
		return k.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (k *Keycloak) accessToken(ctx context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.token != "" && time.Now().Before(k.expiry) {
		return k.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {k.ClientID}, "client_secret": {k.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(k.BaseURL, "/")+"/realms/"+url.PathEscape(k.Realm)+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := k.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("identity: token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("identity: token request failed: status %d (check the admin client id and secret)", resp.StatusCode)
	}
	var t struct {
		Access  string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil || t.Access == "" {
		return "", errors.New("identity: unreadable token response")
	}
	k.token = t.Access
	k.expiry = time.Now().Add(time.Duration(max(t.Expires-30, 1)) * time.Second)
	return k.token, nil
}

// DeleteUser removes the user whose subject (the token's "sub") is given. 404 counts as success.
// If the provider rejects our cached access token (restarted, keys changed, revoked), a fresh one is
// fetched and the call is repeated once before giving up.
func (k *Keycloak) DeleteUser(ctx context.Context, subject string) error {
	if subject == "" || strings.ContainsAny(subject, "/?#\\") {
		return errors.New("identity: refusing suspicious subject")
	}
	err := k.deleteOnce(ctx, subject)
	if errors.Is(err, errTokenRejected) {
		err = k.deleteOnce(ctx, subject)
	}
	if errors.Is(err, errTokenRejected) {
		return errors.New("identity: delete refused (401): token rejected (check the worker's Keycloak client and its manage-users role)")
	}
	return err
}

var errTokenRejected = errors.New("token rejected")

func (k *Keycloak) deleteOnce(ctx context.Context, subject string) error {
	tok, err := k.accessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(k.BaseURL, "/")+"/admin/realms/"+url.PathEscape(k.Realm)+"/users/"+url.PathEscape(subject), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := k.client().Do(req)
	if err != nil {
		return fmt.Errorf("identity: delete request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusNotFound:
		return nil
	case http.StatusUnauthorized:
		k.mu.Lock()
		k.token = "" // expired or revoked: fetch a new one next time
		k.mu.Unlock()
		return errTokenRejected
	default:
		return fmt.Errorf("identity: delete failed: status %d", resp.StatusCode)
	}
}
