package httpapi

import (
	"bytes"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/devices"
)

func withBodies(max int) func(*Deps) {
	return func(d *Deps) { d.LogBodies = BodyLog{Enabled: true, MaxBytes: max} }
}

func TestBodiesAreNotLoggedByDefault(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, nil)
	do(h, "POST", "/api/v1/purchases", "token-alice", `{"productName":"Passport Laptop","purchaseDate":"2026-01-31"}`)
	m := requestLine(t, logLines(t, buf))
	if _, ok := m["body"]; ok {
		t.Fatalf("body logged while the switch is off: %v", m)
	}
	if _, ok := m["path"]; ok {
		t.Fatalf("path logged while the switch is off: %v", m)
	}
}

func TestBodyIsLoggedAsJSONAndHandlerStillGetsIt(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, withBodies(0))
	w := do(h, "POST", "/api/v1/purchases", "token-alice", `{"productName":"Passport Laptop","purchaseDate":"2026-01-31","price":1500000}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("handler no longer works with body logging on: %d %s", w.Code, w.Body)
	}
	m := requestLine(t, logLines(t, buf))
	body, ok := m["body"].(map[string]any)
	if !ok {
		t.Fatalf("body is not nested JSON: %T %v", m["body"], m["body"])
	}
	if body["productName"] != "Passport Laptop" || body["price"] != float64(1500000) {
		t.Errorf("body = %v", body)
	}
	if m["route"] != "/api/v1/purchases" || m["path"] != "/api/v1/purchases" || m["method"] != "POST" {
		t.Errorf("endpoint fields wrong: %v", m)
	}
	if strings.Contains(buf.String(), "token-alice") {
		t.Errorf("token leaked: %s", buf)
	}
}

func TestSecretsInBodiesAreReplaced(t *testing.T) {
	mem := &memDevices{rows: map[uuid.UUID]devices.Device{}, own: map[uuid.UUID]uuid.UUID{}}
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { withBodies(0)(d); d.Devices = mem })
	w := do(h, "POST", "/api/v1/devices", "token-alice", `{"platform":"android","pushToken":"fcm-secret-token-123","nested":{"password":"hunter2","list":[{"access_token":"abc"}]}}`)
	_ = w
	out := buf.String()
	for _, leak := range []string{"fcm-secret-token-123", "hunter2", `"abc"`} {
		if strings.Contains(out, leak) {
			t.Errorf("log leaks %q: %s", leak, out)
		}
	}
	m := requestLine(t, logLines(t, buf))
	body := m["body"].(map[string]any)
	if body["platform"] != "android" || body["pushToken"] != "[REDACTED]" {
		t.Errorf("body = %v", body)
	}
}

func TestLongAndInvalidBodiesAreCut(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, withBodies(80))
	long := `{"productName":"` + strings.Repeat("x", 500) + `","purchaseDate":"2026-01-31"}`
	do(h, "POST", "/api/v1/purchases", "token-alice", long)
	m := requestLine(t, logLines(t, buf))
	s, _ := m["body"].(string)
	if len(s) == 0 || len(s) > 80 || m["body_truncated"] != true {
		t.Errorf("long body not cut: len=%d %v", len(s), m["body_truncated"])
	}

	buf.Reset()
	do(h, "POST", "/api/v1/purchases", "token-alice", `{not json`)
	m = requestLine(t, logLines(t, buf))
	if m["body"] != "{not json" || m["body_invalid_json"] != true {
		t.Errorf("invalid body: %v", m)
	}
}

func TestOversizeBodyIsStillRefusedByTheHandler(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, withBodies(0))
	big := `{"productName":"` + strings.Repeat("x", maxBodyBytes+10) + `","purchaseDate":"2026-01-31"}`
	w := do(h, "POST", "/api/v1/purchases", "token-alice", big)
	if w.Code != http.StatusRequestEntityTooLarge && w.Code != http.StatusBadRequest {
		t.Fatalf("oversize body accepted: %d", w.Code)
	}
	m := requestLine(t, logLines(t, buf))
	if m["body_omitted"] == nil || m["body"] != nil {
		t.Errorf("oversize body should be omitted from the log: %v", m)
	}
}

func TestUploadsAndGetsAreNeverCaptured(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, withBodies(0))

	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	fw, _ := mw.CreateFormFile("file", "struk.png")
	fw.Write([]byte("\x89PNG-binary-secret-bytes"))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/purchases/"+uuid.NewString()+"/receipt", &mp)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer token-alice")
	h.ServeHTTP(httptest.NewRecorder(), req)
	do(h, "GET", "/api/v1/purchases", "token-alice", "")

	out := buf.String()
	if strings.Contains(out, "binary-secret") || strings.Contains(out, "struk.png") {
		t.Errorf("upload leaked: %s", out)
	}
	for _, m := range logLines(t, buf) {
		if m["msg"] == "request" && m["body"] != nil {
			t.Errorf("body captured for %v %v", m["method"], m["route"])
		}
	}
}
