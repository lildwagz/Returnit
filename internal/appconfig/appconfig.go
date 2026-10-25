// Package appconfig tells the mobile app which versions may still talk to the server ("force update") and where
// the newest build can be downloaded. The values live in one small JSON file that the publish script writes, so
// raising the minimum version needs no new release and no restart.
package appconfig

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Platform describes the release of the app for one operating system.
type Platform struct {
	// MinVersion: older versions are refused (the app shows a screen that cannot be closed).
	MinVersion string `json:"minVersion"`
	// LatestVersion: older versions are only invited to update.
	LatestVersion string `json:"latestVersion"`
	DownloadURL   string `json:"downloadUrl"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
	PublishedAt   string `json:"publishedAt"`
}

// Config is the content of the file. An empty or missing file means "no restrictions".
type Config struct {
	Android Platform `json:"android"`
	IOS     Platform `json:"ios"`
	// Message is shown with an update screen; MaintenanceMessage with the maintenance screen.
	Message            string `json:"message"`
	Maintenance        bool   `json:"maintenance"`
	MaintenanceMessage string `json:"maintenanceMessage"`
}

type Status string

const (
	StatusOK              Status = "ok"
	StatusUpdateAvailable Status = "update_available"
	StatusUpdateRequired  Status = "update_required"
	StatusMaintenance     Status = "maintenance"
)

// Provider is what the HTTP layer needs.
type Provider interface{ Current() Config }

// For returns the release data of a platform ("android" or "ios"); an unknown platform has no restrictions.
func (c Config) For(platform string) Platform {
	switch strings.ToLower(platform) {
	case "android":
		return c.Android
	case "ios":
		return c.IOS
	}
	return Platform{}
}

// Evaluate decides what a given app version must do. An empty or unreadable version is never blocked, because
// refusing a client that does not say which version it is would lock out tools and older builds by accident.
// Maintenance wins over everything.
func (c Config) Evaluate(platform, version string) Status {
	if c.Maintenance {
		return StatusMaintenance
	}
	p := c.For(platform)
	if p.MinVersion != "" {
		if cmp, ok := Compare(version, p.MinVersion); ok && cmp < 0 {
			return StatusUpdateRequired
		}
	}
	if p.LatestVersion != "" {
		if cmp, ok := Compare(version, p.LatestVersion); ok && cmp < 0 {
			return StatusUpdateAvailable
		}
	}
	return StatusOK
}

// Compare orders two dotted numeric versions such as "1.4.0" (a missing part counts as 0, and anything after a
// '-' or '+' such as "-beta" is ignored). ok is false when either side is not a version.
func Compare(a, b string) (cmp int, ok bool) {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return 0, false
	}
	for i := 0; i < 4; i++ {
		switch {
		case pa[i] < pb[i]:
			return -1, true
		case pa[i] > pb[i]:
			return 1, true
		}
	}
	return 0, true
}

func parse(v string) (out [4]int, ok bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return out, false
	}
	parts := strings.Split(v, ".")
	if len(parts) > 4 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// FileSource reads the JSON file, at most once per ttl. A file that becomes unreadable keeps the last good content
// (and is logged once), so a mistake while editing never locks users out or lets old versions in.
type FileSource struct {
	path string
	ttl  time.Duration
	log  *slog.Logger
	now  func() time.Time

	mu       sync.Mutex
	cfg      Config
	loadedAt time.Time
	failed   bool
}

// NewFileSource reads path; an empty path means the feature is off (no restrictions).
func NewFileSource(path string, ttl time.Duration, log *slog.Logger) *FileSource {
	if log == nil {
		log = slog.Default()
	}
	return &FileSource{path: path, ttl: ttl, log: log, now: time.Now}
}

func (s *FileSource) Current() Config {
	if s == nil || s.path == "" {
		return Config{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loadedAt.IsZero() && s.now().Sub(s.loadedAt) < s.ttl {
		return s.cfg
	}
	s.loadedAt = s.now()
	raw, err := os.ReadFile(s.path)
	if err == nil {
		var c Config
		if err = json.Unmarshal(raw, &c); err == nil {
			s.cfg, s.failed = c, false
			return s.cfg
		}
	}
	if os.IsNotExist(err) {
		s.cfg, s.failed = Config{}, false // no file yet: nothing is restricted
		return s.cfg
	}
	if !s.failed {
		s.log.Error("app config file unreadable; keeping the last good version", "path", s.path, "err", err)
		s.failed = true
	}
	return s.cfg
}
