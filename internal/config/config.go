// Package config loads and validates runtime configuration from the environment.
//
// The web and worker binaries share the database, object storage and Redis settings (Common)
// and each adds only what it needs, so a process fails fast on exactly its own missing config.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/returnit/returnit/internal/queue"
	"github.com/returnit/returnit/internal/ratelimit"
	"github.com/returnit/returnit/internal/storage"
)

// Common is needed by both processes.
type Common struct {
	DatabaseURL     string
	Redis           queue.Config
	S3              storage.Config
	ShutdownTimeout time.Duration
	// LogSQL (LOG_SQL=on) logs every SQL statement with its duration; LogRedis (LOG_REDIS=on) logs what is
	// written to Redis for the worker and every job run. Both are off by default: they are very chatty.
	LogSQL   bool
	LogRedis bool
}

// onOff reads an on/off environment switch (unset means off).
func onOff(name string) (bool, error) {
	switch os.Getenv(name) {
	case "", "off":
		return false, nil
	case "on":
		return true, nil
	}
	return false, fmt.Errorf("config: %s must be 'on' or 'off'", name)
}

// Web is the HTTP API process.
type Web struct {
	Common
	HTTPAddr       string
	RequestTimeout time.Duration
	OIDCIssuer     string
	OIDCAudience   string
	// OIDCDiscoveryURL, if set, is where the provider metadata is fetched from (an internal
	// address); tokens must still carry OIDCIssuer. Empty means fetch from the issuer.
	OIDCDiscoveryURL string
	// MalwareScan is "on" (default) or "off". Off is for local development only.
	MalwareScan     string
	ClamAVAddr      string
	MaxReceiptBytes int64
	// AccountDeletionGrace is how long after a deletion request the account is erased.
	AccountDeletionGrace time.Duration
	// FreshLoginMaxAge: how recent the login must be to request account deletion.
	FreshLoginMaxAge time.Duration
	// LogRequestBody turns on logging of JSON request bodies (LOG_REQUEST_BODY=on); off by default because
	// bodies hold personal data. LogRequestBodyMax is the longest body text kept per line.
	LogRequestBody    bool
	LogRequestBodyMax int
	RateLimits        ratelimit.Limits
	// TrustedProxies lists reverse-proxy IPs/CIDRs whose X-Forwarded-For is believed (default: none).
	TrustedProxies []string
}

// Worker is the background-job process (reminders, retention, push delivery).
type Worker struct {
	Common
	Concurrency int
	// OrphanGrace protects in-flight uploads from the orphan sweep.
	OrphanGrace time.Duration
	// HealthAddr is where the health/metrics server listens. Keep it off the public internet.
	HealthAddr string
	// SMTP is where deletion notices are sent; SMTP.Addr empty means e-mail is not configured
	// (notices stay queued and the worker says so at startup).
	SMTP SMTP
	// Keycloak, when set, lets the worker delete the login identity of erased accounts.
	Keycloak Keycloak
	// FCM, when set, enables mobile push notifications (Firebase Cloud Messaging).
	FCMProjectID, FCMCredentialsFile string
}

type Keycloak struct {
	BaseURL, Realm, ClientID, ClientSecret string
}

type SMTP struct {
	Addr, From, Username, Password string
	TLS                            string // starttls (default), tls or none
}

func LoadWeb() (Web, error) {
	common, err := loadCommon()
	if err != nil {
		return Web{}, err
	}
	w := Web{
		Common:           common,
		HTTPAddr:         getenv("HTTP_ADDR", ":8080"),
		RequestTimeout:   15 * time.Second,
		OIDCIssuer:       os.Getenv("OIDC_ISSUER"),
		OIDCAudience:     os.Getenv("OIDC_AUDIENCE"),
		OIDCDiscoveryURL: os.Getenv("OIDC_DISCOVERY_URL"),
		MalwareScan:      getenv("MALWARE_SCAN", "on"),
		ClamAVAddr:       os.Getenv("CLAMAV_ADDR"),
		MaxReceiptBytes:  10 << 20,

		AccountDeletionGrace: 7 * 24 * time.Hour,
		FreshLoginMaxAge:     5 * time.Minute,
		RateLimits:           ratelimit.DefaultLimits(),
	}
	if w.OIDCIssuer == "" || w.OIDCAudience == "" {
		return Web{}, errors.New("config: OIDC_ISSUER and OIDC_AUDIENCE are required")
	}
	switch w.MalwareScan {
	case "on":
		if w.ClamAVAddr == "" {
			return Web{}, errors.New("config: CLAMAV_ADDR is required when MALWARE_SCAN=on (set MALWARE_SCAN=off only for local development)")
		}
	case "off":
	default:
		return Web{}, errors.New("config: MALWARE_SCAN must be 'on' or 'off'")
	}
	if v := os.Getenv("MAX_RECEIPT_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return Web{}, errors.New("config: MAX_RECEIPT_BYTES must be a positive integer")
		}
		w.MaxReceiptBytes = n
	}
	if v := os.Getenv("ACCOUNT_DELETION_GRACE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Hour || d > 90*24*time.Hour {
			return Web{}, errors.New("config: ACCOUNT_DELETION_GRACE must be a duration between 1h and 2160h (90 days)")
		}
		w.AccountDeletionGrace = d
	}
	switch os.Getenv("LOG_REQUEST_BODY") {
	case "", "off":
	case "on":
		w.LogRequestBody = true
	default:
		return Web{}, errors.New("config: LOG_REQUEST_BODY must be 'on' or 'off'")
	}
	w.LogRequestBodyMax = 2048
	if v := os.Getenv("LOG_REQUEST_BODY_MAX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 64 || n > 65536 {
			return Web{}, errors.New("config: LOG_REQUEST_BODY_MAX must be between 64 and 65536")
		}
		w.LogRequestBodyMax = n
	}
	if v := os.Getenv("FRESH_LOGIN_MAX_AGE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute || d > time.Hour {
			return Web{}, errors.New("config: FRESH_LOGIN_MAX_AGE must be a duration between 1m and 1h")
		}
		w.FreshLoginMaxAge = d
	}
	for _, l := range []struct {
		env string
		dst *int
	}{
		{"RATE_LIMIT_USER_PER_MIN", &w.RateLimits.UserPerMinute},
		{"RATE_LIMIT_UPLOAD_PER_MIN", &w.RateLimits.UploadPerMinute},
		{"RATE_LIMIT_ACCOUNT_DELETE_PER_HOUR", &w.RateLimits.AccountDeletionPerHour},
		{"RATE_LIMIT_UNAUTH_PER_MIN_PER_IP", &w.RateLimits.UnauthPerMinutePerIP},
		{"RATE_LIMIT_EXPORT_PER_HOUR", &w.RateLimits.ExportPerHour},
	} {
		if v := os.Getenv(l.env); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 100000 {
				return Web{}, fmt.Errorf("config: %s must be an integer between 1 and 100000", l.env)
			}
			*l.dst = n
		}
	}
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(p); err != nil && net.ParseIP(p) == nil {
				return Web{}, fmt.Errorf("config: TRUSTED_PROXIES entry %q is not an IP or CIDR", p)
			}
			w.TrustedProxies = append(w.TrustedProxies, p)
		}
	}
	return w, nil
}

func LoadWorker() (Worker, error) {
	common, err := loadCommon()
	if err != nil {
		return Worker{}, err
	}
	w := Worker{Common: common, Concurrency: 10, OrphanGrace: 24 * time.Hour, HealthAddr: getenv("WORKER_HEALTH_ADDR", ":8081")}
	if _, _, err := net.SplitHostPort(w.HealthAddr); err != nil {
		return Worker{}, errors.New("config: WORKER_HEALTH_ADDR must look like :8081 or 127.0.0.1:8081")
	}
	if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return Worker{}, errors.New("config: WORKER_CONCURRENCY must be an integer between 1 and 200")
		}
		w.Concurrency = n
	}
	if v := os.Getenv("RETENTION_ORPHAN_GRACE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Hour {
			return Worker{}, errors.New("config: RETENTION_ORPHAN_GRACE must be a duration >= 1h")
		}
		w.OrphanGrace = d
	}
	w.SMTP = SMTP{Addr: os.Getenv("SMTP_ADDR"), From: os.Getenv("SMTP_FROM"), Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"), TLS: strings.ToLower(getenv("SMTP_TLS", "starttls"))}
	if w.SMTP.Addr != "" {
		if _, _, err := net.SplitHostPort(w.SMTP.Addr); err != nil {
			return Worker{}, errors.New("config: SMTP_ADDR must look like host:587")
		}
		if _, err := mail.ParseAddress(w.SMTP.From); err != nil || strings.ContainsAny(w.SMTP.From, "\r\n<>") {
			return Worker{}, errors.New("config: SMTP_FROM must be a plain address like noreply@example.com when SMTP_ADDR is set")
		}
		switch w.SMTP.TLS {
		case "starttls", "tls", "none":
		default:
			return Worker{}, errors.New("config: SMTP_TLS must be starttls, tls or none")
		}
		if w.SMTP.TLS == "none" && w.SMTP.Username != "" {
			return Worker{}, errors.New("config: refusing to send an SMTP password without TLS (SMTP_TLS=none is for local development without credentials)")
		}
	}
	w.FCMProjectID, w.FCMCredentialsFile = os.Getenv("FCM_PROJECT_ID"), os.Getenv("FCM_CREDENTIALS_FILE")
	if (w.FCMProjectID == "") != (w.FCMCredentialsFile == "") {
		return Worker{}, errors.New("config: FCM_PROJECT_ID and FCM_CREDENTIALS_FILE must be set together")
	}
	w.Keycloak = Keycloak{BaseURL: os.Getenv("KEYCLOAK_URL"), Realm: os.Getenv("KEYCLOAK_REALM"),
		ClientID: os.Getenv("KEYCLOAK_ADMIN_CLIENT_ID"), ClientSecret: os.Getenv("KEYCLOAK_ADMIN_CLIENT_SECRET")}
	if k := w.Keycloak; k.BaseURL != "" || k.Realm != "" || k.ClientID != "" || k.ClientSecret != "" {
		if k.BaseURL == "" || k.Realm == "" || k.ClientID == "" || k.ClientSecret == "" {
			return Worker{}, errors.New("config: KEYCLOAK_URL, KEYCLOAK_REALM, KEYCLOAK_ADMIN_CLIENT_ID and KEYCLOAK_ADMIN_CLIENT_SECRET must all be set together")
		}
		if u, err := url.Parse(k.BaseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return Worker{}, errors.New("config: KEYCLOAK_URL must be an http(s) URL")
		}
	}
	return w, nil
}

func loadCommon() (Common, error) {
	c := Common{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		ShutdownTimeout: 10 * time.Second,
		Redis: queue.Config{
			Addr:     os.Getenv("REDIS_ADDR"),
			Password: os.Getenv("REDIS_PASSWORD"),
		},
		S3: storage.Config{
			Endpoint:  os.Getenv("S3_ENDPOINT"),
			AccessKey: os.Getenv("S3_ACCESS_KEY"),
			SecretKey: os.Getenv("S3_SECRET_KEY"),
			Bucket:    getenv("S3_BUCKET", "returnit-receipts"),
			Region:    getenv("S3_REGION", "us-east-1"),
			UseSSL:    os.Getenv("S3_USE_SSL") == "true",

			PublicEndpoint: os.Getenv("S3_PUBLIC_ENDPOINT"),
			PublicUseSSL:   os.Getenv("S3_PUBLIC_USE_SSL") != "false", // HTTPS unless told otherwise
		},
	}
	var err error
	if c.LogSQL, err = onOff("LOG_SQL"); err != nil {
		return Common{}, err
	}
	if c.LogRedis, err = onOff("LOG_REDIS"); err != nil {
		return Common{}, err
	}
	if c.DatabaseURL == "" {
		return Common{}, errors.New("config: DATABASE_URL is required")
	}
	if c.Redis.Addr == "" {
		return Common{}, errors.New("config: REDIS_ADDR is required")
	}
	if v := os.Getenv("REDIS_DB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return Common{}, errors.New("config: REDIS_DB must be a non-negative integer")
		}
		c.Redis.DB = n
	}
	if c.S3.Endpoint == "" || c.S3.AccessKey == "" || c.S3.SecretKey == "" {
		return Common{}, errors.New("config: S3_ENDPOINT, S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}
	if v := os.Getenv("SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Common{}, fmt.Errorf("config: invalid SHUTDOWN_TIMEOUT: %w", err)
		}
		c.ShutdownTimeout = d
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
