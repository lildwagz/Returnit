package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

const tok = "device-token-0123456789-SECRET"

func server(t *testing.T, status int, reply string) (*FCM, *map[string]any, *http.Header) {
	t.Helper()
	var got map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		if r.URL.Path != "/v1/projects/my-proj/messages:send" || r.Method != "POST" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return &FCM{ProjectID: "my-proj", BaseURL: srv.URL, Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "access-123"})}, &got, &hdr
}

func TestSendSuccessAndPayload(t *testing.T) {
	f, got, hdr := server(t, 200, `{"name":"projects/my-proj/messages/1"}`)
	err := f.Send(context.Background(), tok, Message{ID: "n-1", Title: "Warranty ends soon", Body: "Laptop: 7 days left"})
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Authorization") != "Bearer access-123" {
		t.Errorf("auth header %q", hdr.Get("Authorization"))
	}
	m := (*got)["message"].(map[string]any)
	n := m["notification"].(map[string]any)
	d := m["data"].(map[string]any)
	if m["token"] != tok || n["title"] != "Warranty ends soon" || n["body"] != "Laptop: 7 days left" || d["notificationId"] != "n-1" {
		t.Errorf("payload: %v", m)
	}
	if m["android"].(map[string]any)["priority"] != "HIGH" {
		t.Error("android priority must be high so reminders are not delayed")
	}
}

func TestClassifyErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error // nil = temporary
	}{
		{"unregistered", 404, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, ErrUnregistered},
		{"unregistered without details", 404, `{}`, ErrUnregistered},
		{"malformed token", 400, `{"error":{"status":"INVALID_ARGUMENT","details":[{"fieldViolations":[{"field":"message.token"}]}]}}`, ErrUnregistered},
		{"other bad request", 400, `{"error":{"status":"INVALID_ARGUMENT","message":"payload too big"}}`, ErrRejected},
		{"sender mismatch", 403, `{"error":{"status":"PERMISSION_DENIED","details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`, ErrUnregistered},
		{"quota", 429, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, nil},
		{"server", 503, `{"error":{"status":"UNAVAILABLE"}}`, nil},
		{"bad credentials", 401, `{"error":{"status":"UNAUTHENTICATED"}}`, nil},
	}
	for _, c := range cases {
		f, _, _ := server(t, c.status, c.body)
		err := f.Send(context.Background(), tok, Message{ID: "x", Title: "t", Body: "b"})
		switch {
		case err == nil:
			t.Errorf("%s: expected an error", c.name)
		case c.want != nil && !errors.Is(err, c.want):
			t.Errorf("%s: want %v, got %v", c.name, c.want, err)
		case c.want == nil && (errors.Is(err, ErrUnregistered) || errors.Is(err, ErrRejected)):
			t.Errorf("%s: must be treated as temporary, got %v", c.name, err)
		}
		if err != nil && strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: error leaks the device token: %v", c.name, err)
		}
	}
}

func TestTransportErrorDoesNotLeakToken(t *testing.T) {
	f := &FCM{ProjectID: "p", BaseURL: "http://127.0.0.1:1", Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "a"})}
	err := f.Send(context.Background(), tok, Message{ID: "x"})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("transport error: %v", err)
	}
}

func TestTokenSourceFailureIsTemporary(t *testing.T) {
	f := &FCM{ProjectID: "p", Tokens: failingSource{}}
	err := f.Send(context.Background(), tok, Message{ID: "x"})
	if err == nil || errors.Is(err, ErrUnregistered) || errors.Is(err, ErrRejected) {
		t.Errorf("credential problem must be retryable and not delete tokens: %v", err)
	}
}

type failingSource struct{}

func (failingSource) Token() (*oauth2.Token, error) { return nil, errors.New("no creds") }

func TestNewFCMRejectsGarbage(t *testing.T) {
	if _, err := NewFCM(context.Background(), "p", []byte("not json")); err == nil {
		t.Error("garbage credentials must be rejected at start-up")
	}
}

func TestNewFCMAcceptsServiceAccountKey(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	creds, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "p", "private_key_id": "k",
		"private_key": string(pemKey), "client_email": "sa@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	f, err := NewFCM(context.Background(), "p", creds)
	if err != nil || f.Tokens == nil {
		t.Fatalf("valid key rejected: %v", err)
	}
}
