// Package httpapi wires the Gin router, middleware and handlers.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/auth"
	"github.com/returnit/returnit/internal/ratelimit"
)

// Pinger is satisfied by *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// UserResolver maps a verified identity to a local user ID.
type UserResolver interface {
	EnsureBySubject(ctx context.Context, sub, email, name string) (uuid.UUID, error)
}

type Deps struct {
	DB            Pinger
	Verifier      auth.Verifier
	Users         UserResolver
	Purchases     PurchaseService
	Receipts      ReceiptService
	Reminders     ReminderService
	Notifications NotificationService
	Account       AccountService
	Logger        *slog.Logger // nil means slog.Default()
	// Limiter backs rate limiting; nil means an in-memory limiter (per process).
	Limiter ratelimit.Limiter
	// Limits are the per-bucket budgets; the zero value means ratelimit.DefaultLimits().
	Limits ratelimit.Limits
	// TrustedProxies are the CIDRs/IPs of reverse proxies whose X-Forwarded-For may be believed.
	// Empty (the default) means the socket address is always used, so a client cannot spoof its IP.
	TrustedProxies []string
	Devices        DeviceService
	RequestTimeout time.Duration
	// FreshLoginMaxAge is how recent the login must be to request account deletion; 0 means 5 minutes.
	FreshLoginMaxAge time.Duration
	// LogBodies turns on request-body logging (off by default; see BodyLog).
	LogBodies BodyLog
}

const requestIDHeader = "X-Request-ID"

func NewRouter(d Deps) *gin.Engine {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	limiter := d.Limiter
	if limiter == nil {
		limiter = ratelimit.NewMemory()
	}
	limits := d.Limits.WithDefaults()
	r := gin.New()
	if err := r.SetTrustedProxies(d.TrustedProxies); err != nil {
		// Misconfiguration must not silently trust every proxy; config validation reports it first.
		log.Error("invalid trusted proxies; trusting none", "err", err)
		_ = r.SetTrustedProxies(nil)
	}
	// Order matters: the request logger wraps recovery, so a panic is logged both with its stack
	// and as a 500 request line.
	r.Use(requestID(), requestLogger(log, d.LogBodies), recovery(), timeout(d.RequestTimeout))

	r.GET("/health/live", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, ErrorBody("not_ready", "database unavailable", c))
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	v1 := r.Group("/api/v1",
		authRequired(d.Verifier, d.Users, authFailureLimit(limiter, limits.UnauthPerMinutePerIP)),
		userLimit(limiter, "api", limits.UserPerMinute, time.Minute, true))
	registerPurchases(v1, d.Purchases)
	registerReceipts(v1, d.Receipts, userLimit(limiter, "upload", limits.UploadPerMinute, time.Minute, false))
	registerReminders(v1, d.Reminders)
	registerNotifications(v1, d.Notifications)
	registerDevices(v1, d.Devices)
	freshAge := d.FreshLoginMaxAge
	if freshAge <= 0 {
		freshAge = 5 * time.Minute
	}
	registerAccount(v1, d.Account, freshLogin(freshAge, time.Now),
		userLimit(limiter, "account-deletion", limits.AccountDeletionPerHour, time.Hour, false),
		userLimit(limiter, "export", limits.ExportPerHour, time.Hour, false))

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "route not found", c))
	})
	return r
}

// ErrorBody builds the standard error envelope.
func ErrorBody(code, msg string, c *gin.Context) gin.H {
	return gin.H{"error": gin.H{"code": code, "message": msg, "request_id": c.GetString("request_id")}}
}

func timeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
