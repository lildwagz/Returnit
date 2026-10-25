package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/returnit/returnit/internal/appconfig"
	"github.com/returnit/returnit/internal/ratelimit"
)

const (
	appVersionHeader  = "X-App-Version"
	appPlatformHeader = "X-App-Platform"
)

func currentAppConfig(p appconfig.Provider) appconfig.Config {
	if p == nil {
		return appconfig.Config{}
	}
	return p.Current()
}

// registerAppConfig serves GET /api/v1/app-config, which needs no login: the app asks it before the login screen.
// It is limited per client IP, like every other call that has no user to charge.
func registerAppConfig(r *gin.Engine, p appconfig.Provider, l ratelimit.Limiter, perMinute int) {
	r.GET("/api/v1/app-config", func(c *gin.Context) {
		if _, ok := allow(c, l, "appconfig:"+c.ClientIP(), perMinute, time.Minute); !ok {
			tooManyRequests(c, "appconfig", ratelimit.Result{RetryAfter: time.Minute, Limit: perMinute})
			return
		}
		platform := strings.ToLower(c.DefaultQuery("platform", "android"))
		version := c.Query("version")
		if version == "" {
			version = c.GetHeader(appVersionHeader)
		}
		cfg := currentAppConfig(p)
		rel := cfg.For(platform)
		status := cfg.Evaluate(platform, version)

		body := gin.H{
			"status":        status,
			"platform":      platform,
			"minVersion":    rel.MinVersion,
			"latestVersion": rel.LatestVersion,
			"downloadUrl":   rel.DownloadURL,
			"sha256":        rel.SHA256,
			"sizeBytes":     rel.SizeBytes,
			"publishedAt":   rel.PublishedAt,
		}
		switch status {
		case appconfig.StatusMaintenance:
			body["message"] = cfg.MaintenanceMessage
		case appconfig.StatusUpdateRequired, appconfig.StatusUpdateAvailable:
			body["message"] = cfg.Message
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, body)
	})
}

// appVersionGate refuses API calls from app versions below the minimum (426) and from every version during
// maintenance (503). A request that does not say its version (curl, tests, builds from before this existed) is
// let through: this protects against a broken old release, it is not a security control, because the header
// can be set to anything.
func appVersionGate(p appconfig.Provider) gin.HandlerFunc {
	return func(c *gin.Context) {
		version := c.GetHeader(appVersionHeader)
		cfg := currentAppConfig(p)
		if version == "" && !cfg.Maintenance {
			c.Next()
			return
		}
		platform := c.GetHeader(appPlatformHeader)
		if platform == "" {
			platform = "android"
		}
		switch cfg.Evaluate(platform, version) {
		case appconfig.StatusMaintenance:
			msg := cfg.MaintenanceMessage
			if msg == "" {
				msg = "the service is under maintenance, try again later"
			}
			c.Header("Retry-After", "300")
			_ = c.Error(errAppGate("maintenance"))
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, ErrorBody("maintenance", msg, c))
		case appconfig.StatusUpdateRequired:
			rel := cfg.For(platform)
			msg := cfg.Message
			if msg == "" {
				msg = "this version of the app is no longer supported, please update"
			}
			c.Header("X-Min-App-Version", rel.MinVersion)
			_ = c.Error(errAppGate("update required"))
			body := ErrorBody("app_update_required", msg, c)
			body["error"].(gin.H)["minVersion"] = rel.MinVersion
			body["error"].(gin.H)["downloadUrl"] = rel.DownloadURL
			c.AbortWithStatusJSON(http.StatusUpgradeRequired, body)
		default:
			c.Next()
		}
	}
}

// errAppGate records why a request was refused, so the request log line says so.
type errAppGate string

func (e errAppGate) Error() string { return "app version gate: " + string(e) }
