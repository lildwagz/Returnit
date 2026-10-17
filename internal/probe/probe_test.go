package probe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURL(t *testing.T) {
	cases := map[string]string{
		":8081":          "http://127.0.0.1:8081/health/ready",
		"0.0.0.0:8081":   "http://127.0.0.1:8081/health/ready",
		"[::]:8081":      "http://127.0.0.1:8081/health/ready",
		"127.0.0.1:9000": "http://127.0.0.1:9000/health/ready",
		"garbage":        "http://127.0.0.1:8080/health/ready",
	}
	for in, want := range cases {
		if got := URL(in, "/health/ready"); got != want {
			t.Errorf("URL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRun(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	if code, msg := Run(ok.URL); code != 0 || msg != "healthy" {
		t.Errorf("200: %d %q", code, msg)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if code, msg := Run(bad.URL); code != 1 || !strings.Contains(msg, "503") {
		t.Errorf("503: %d %q", code, msg)
	}

	if code, _ := Run("http://127.0.0.1:1/health/ready"); code != 1 {
		t.Errorf("unreachable must be unhealthy: %d", code)
	}
	// The probe is a local-only tool: it refuses to be pointed at other hosts.
	if code, msg := Run("http://example.com/health"); code != 1 || !strings.Contains(msg, "local") {
		t.Errorf("remote URL accepted: %d %q", code, msg)
	}
}
