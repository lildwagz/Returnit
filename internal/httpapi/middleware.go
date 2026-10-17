package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/auth"
	"github.com/returnit/returnit/internal/ratelimit"
	usersdomain "github.com/returnit/returnit/internal/users"
)

const (
	ctxUserID = "user_id"
	ctxClaims = "claims"
)

// authRequired verifies the bearer token and stores the local user ID. The ID always
// comes from the verified token, never from the request body or query.
//
// Every failed attempt is charged to the client IP (see authFailureLimit); once an IP is over its
// budget, further failures get 429 instead of 401.
func authRequired(v auth.Verifier, users UserResolver, charge func(*gin.Context) (ratelimit.Result, bool)) gin.HandlerFunc {
	reject := func(c *gin.Context, msg string, cause error) {
		if cause != nil {
			_ = c.Error(cause) // reason (expired, wrong audience, ...) goes to the log, never the token
		}
		if res, ok := charge(c); !ok {
			tooManyRequests(c, "unauthenticated-ip", res)
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorBody("unauthorized", msg, c))
	}
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		raw, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || strings.TrimSpace(raw) == "" {
			reject(c, "missing bearer token", nil)
			return
		}
		claims, err := v.Verify(c.Request.Context(), strings.TrimSpace(raw))
		if err != nil {
			reject(c, "invalid token", err)
			return
		}
		id, err := users.EnsureBySubject(c.Request.Context(), claims.Subject, claims.Email, claims.DisplayName)
		if errors.Is(err, usersdomain.ErrIdentityConflict) {
			_ = c.Error(err)
			c.AbortWithStatusJSON(http.StatusConflict, ErrorBody("identity_conflict",
				"this e-mail address is already linked to a different login; please contact support", c))
			return
		}
		if err != nil {
			_ = c.Error(err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorBody("internal", "internal error", c))
			return
		}
		c.Set(ctxUserID, id)
		c.Set(ctxClaims, claims)
		c.Next()
	}
}

func userID(c *gin.Context) uuid.UUID { return c.MustGet(ctxUserID).(uuid.UUID) }

// freshLogin protects dangerous actions: the token must prove the user logged in within maxAge.
// A stolen or long-lived session is therefore not enough. The client should send the user through
// a new login (OIDC prompt=login / max_age) and retry with the new token.
func freshLogin(maxAge time.Duration, now func() time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		cl, _ := c.Get(ctxClaims)
		claims, _ := cl.(auth.Claims)
		// Missing auth_time counts as stale: fail closed.
		if claims.AuthTime.IsZero() || now().Sub(claims.AuthTime) > maxAge {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				ErrorBody("reauthentication_required", "please log in again to confirm this action", c))
			return
		}
		c.Next()
	}
}
