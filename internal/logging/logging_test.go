package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func record(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("log line is not JSON: %v: %q", err, buf.String())
	}
	return m
}

func TestServiceNameAndRedaction(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "web", slog.LevelInfo)
	l.Info("hello",
		"Authorization", "Bearer abc.def.ghi", "signed_url", "https://s3/x?X-Amz-Signature=zzz",
		"email", "a@b.c", "file_name", "passport.pdf", "request_id", "r-1")
	m := record(t, &buf)
	if m["service"] != "web" || m["request_id"] != "r-1" {
		t.Fatalf("missing service/request_id: %v", m)
	}
	for _, k := range []string{"Authorization", "signed_url", "email", "file_name"} {
		if m[k] != "[REDACTED]" {
			t.Errorf("%s not redacted: %v", k, m[k])
		}
	}
	for _, leak := range []string{"abc.def.ghi", "X-Amz-Signature", "a@b.c", "passport"} {
		if strings.Contains(buf.String(), leak) {
			t.Errorf("log line leaks %q: %s", leak, buf.String())
		}
	}
}

func TestLevels(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "web", ParseLevel("warn"))
	l.Info("dropped")
	if buf.Len() != 0 {
		t.Fatal("info must be filtered at warn level")
	}
	l.Warn("kept")
	if buf.Len() == 0 {
		t.Fatal("warn must be logged")
	}
	cases := map[string]slog.Level{"": slog.LevelInfo, "bogus": slog.LevelInfo, "DEBUG": slog.LevelDebug, " error ": slog.LevelError, "warning": slog.LevelWarn}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestContextLogger(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "web", slog.LevelInfo).With("request_id", "r-9")
	FromContext(WithContext(context.Background(), l)).Info("x")
	if record(t, &buf)["request_id"] != "r-9" {
		t.Fatal("request-scoped logger not returned")
	}
	if FromContext(context.Background()) == nil {
		t.Fatal("must fall back to the default logger")
	}
}
