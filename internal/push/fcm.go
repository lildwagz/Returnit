// Package push sends mobile push notifications through Firebase Cloud Messaging (HTTP v1 API).
// FCM delivers to both Android and iOS (iOS through Apple's push service, configured in Firebase).
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var (
	// ErrUnregistered means FCM says this token is dead (app uninstalled, token rotated). Remove it.
	ErrUnregistered = errors.New("push: token is no longer valid")
	// ErrRejected means FCM refused the message itself; retrying the same message will not help.
	ErrRejected = errors.New("push: message rejected")
)

// Message is what the phone receives. The ID lets the app open the right notification.
type Message struct {
	ID    string
	Title string
	Body  string
}

const scope = "https://www.googleapis.com/auth/firebase.messaging"

type FCM struct {
	ProjectID string
	Tokens    oauth2.TokenSource
	HTTP      *http.Client // nil means a client with a 10s timeout
	// BaseURL is overridable for tests; empty means https://fcm.googleapis.com.
	BaseURL string
}

// NewFCM builds a sender from a Google service-account key (JSON). The service account needs the
// "Firebase Cloud Messaging API Admin" role (or equivalent) on the Firebase project.
func NewFCM(ctx context.Context, projectID string, credentialsJSON []byte) (*FCM, error) {
	creds, err := google.CredentialsFromJSON(ctx, credentialsJSON, scope)
	if err != nil {
		return nil, fmt.Errorf("push: bad FCM credentials: %w", err)
	}
	return &FCM{ProjectID: projectID, Tokens: creds.TokenSource}, nil
}

func (f *FCM) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Send delivers one message to one device token. Errors never contain the token.
func (f *FCM) Send(ctx context.Context, token string, m Message) error {
	tok, err := f.Tokens.Token()
	if err != nil {
		return fmt.Errorf("push: cannot get FCM access token (check the service account): %w", err)
	}
	payload := map[string]any{"message": map[string]any{
		"token":        token,
		"notification": map[string]string{"title": m.Title, "body": m.Body},
		"data":         map[string]string{"notificationId": m.ID, "type": "warranty_reminder"},
		"android": map[string]any{
			"priority":     "HIGH",
			"notification": map[string]string{"channel_id": "reminders"},
		},
		"apns": map[string]any{
			"headers": map[string]string{"apns-priority": "10"},
			"payload": map[string]any{"aps": map[string]string{"sound": "default"}},
		},
	}}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	base := f.BaseURL
	if base == "" {
		base = "https://fcm.googleapis.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(base, "/")+"/v1/projects/"+url.PathEscape(f.ProjectID)+"/messages:send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client().Do(req)
	if err != nil {
		return scrub(err, token)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return classify(resp.StatusCode, raw)
}

// scrub makes sure a transport error cannot carry the token (it travels only in the body).
func scrub(err error, token string) error {
	return errors.New(strings.ReplaceAll(err.Error(), token, "[token]"))
}

type fcmError struct {
	Error struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Details []struct {
			ErrorCode  string `json:"errorCode"`
			Violations []struct {
				Field string `json:"field"`
			} `json:"fieldViolations"`
		} `json:"details"`
	} `json:"error"`
}

func classify(status int, raw []byte) error {
	var e fcmError
	_ = json.Unmarshal(raw, &e)
	code := e.Error.Status
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
		}
	}
	switch {
	case code == "UNREGISTERED" || status == http.StatusNotFound:
		return ErrUnregistered
	case status == http.StatusBadRequest:
		for _, d := range e.Error.Details {
			for _, v := range d.Violations {
				if strings.Contains(v.Field, "token") {
					return fmt.Errorf("%w: %s", ErrUnregistered, "malformed token")
				}
			}
		}
		return fmt.Errorf("%w: FCM said %s", ErrRejected, nonEmpty(code, "bad request"))
	case status == http.StatusForbidden && code == "SENDER_ID_MISMATCH":
		// The token belongs to another Firebase project: it can never work for us.
		return ErrUnregistered
	default:
		// 401/403 (credentials), 429 (quota), 5xx: transient or operator problems; the queue retries.
		return fmt.Errorf("push: FCM temporary failure: status %d %s", status, code)
	}
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
