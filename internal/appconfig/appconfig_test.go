package appconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.0.0", "1.0.0", 0, true},
		{"1.0.0", "1.0.1", -1, true},
		{"1.10.0", "1.9.9", 1, true}, // numeric, not text, order
		{"1.2", "1.2.0", 0, true},
		{"v1.2.0", "1.2.0", 0, true},
		{"1.4.0-beta", "1.4.0", 0, true},
		{"2.0.0", "1.99.99", 1, true},
		{"", "1.0.0", 0, false},
		{"abc", "1.0.0", 0, false},
		{"1.0.0", "1..0", 0, false},
		{"1.2.3.4.5", "1.0.0", 0, false},
	} {
		got, ok := Compare(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("Compare(%q,%q) = %d,%v want %d,%v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

func TestEvaluate(t *testing.T) {
	cfg := Config{Android: Platform{MinVersion: "1.2.0", LatestVersion: "1.4.0"}}
	for _, c := range []struct {
		platform, version string
		want              Status
	}{
		{"android", "1.1.9", StatusUpdateRequired},
		{"android", "1.2.0", StatusUpdateAvailable},
		{"android", "1.4.0", StatusOK},
		{"android", "2.0.0", StatusOK},
		{"android", "", StatusOK},        // unknown version: never blocked
		{"android", "garbage", StatusOK}, // unreadable version: never blocked
		{"ios", "0.0.1", StatusOK},       // nothing configured for iOS
		{"windows", "0.0.1", StatusOK},   // unknown platform
		{"ANDROID", "1.0.0", StatusUpdateRequired},
	} {
		if got := cfg.Evaluate(c.platform, c.version); got != c.want {
			t.Errorf("Evaluate(%q,%q) = %s want %s", c.platform, c.version, got, c.want)
		}
	}
	m := Config{Maintenance: true, Android: Platform{MinVersion: "9.0.0"}}
	if got := m.Evaluate("android", "1.0.0"); got != StatusMaintenance {
		t.Errorf("maintenance must win, got %s", got)
	}
}

func TestFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.json")
	now := time.Now()
	s := NewFileSource(path, 30*time.Second, nil)
	s.now = func() time.Time { return now }

	if got := s.Current(); got.Evaluate("android", "0.0.1") != StatusOK {
		t.Fatal("a missing file must mean no restrictions")
	}

	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"android":{"minVersion":"1.2.0"}}`)
	now = now.Add(time.Minute)
	if got := s.Current().Evaluate("android", "1.0.0"); got != StatusUpdateRequired {
		t.Fatalf("after reload: %s", got)
	}

	// a broken edit keeps the last good content
	write(`{not json`)
	now = now.Add(time.Minute)
	if got := s.Current().Evaluate("android", "1.0.0"); got != StatusUpdateRequired {
		t.Fatalf("broken file must keep the last good config, got %s", got)
	}

	// the ttl: a change is not seen before it expires
	write(`{"android":{"minVersion":"1.0.0"}}`)
	now = now.Add(time.Minute)
	if got := s.Current().Evaluate("android", "1.0.0"); got != StatusOK {
		t.Fatalf("fixed file: %s", got)
	}
	write(`{"android":{"minVersion":"3.0.0"}}`)
	now = now.Add(5 * time.Second)
	if got := s.Current().Evaluate("android", "1.0.0"); got != StatusOK {
		t.Fatalf("cached within ttl: %s", got)
	}

	if got := NewFileSource("", time.Second, nil).Current(); got.Evaluate("android", "0.0.1") != StatusOK {
		t.Fatal("empty path means feature off")
	}
}
