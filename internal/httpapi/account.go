package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/account"
)

type AccountService interface {
	Get(ctx context.Context, userID uuid.UUID) (account.Account, error)
	RequestDeletion(ctx context.Context, userID uuid.UUID) (time.Time, error)
	CancelDeletion(ctx context.Context, userID uuid.UUID) error
	// Export streams the user's data as one JSON document (see account.Service.Export).
	Export(ctx context.Context, userID uuid.UUID, w io.Writer) error
}

func registerAccount(g *gin.RouterGroup, svc AccountService, fresh gin.HandlerFunc, deletionLimit, exportLimit gin.HandlerFunc) {
	g.GET("/account", func(c *gin.Context) {
		a, err := svc.Get(c.Request.Context(), userID(c))
		if err != nil {
			writeAccountErr(c, err)
			return
		}
		c.JSON(http.StatusOK, a)
	})

	// Schedules erasure after the grace period; 202 because nothing is deleted yet.
	g.DELETE("/account", fresh, deletionLimit, func(c *gin.Context) {
		at, err := svc.RequestDeletion(c.Request.Context(), userID(c))
		if err != nil {
			writeAccountErr(c, err)
			return
		}
		logger(c).Info("account deletion scheduled", "user_id", userID(c), "scheduled_for", at)
		c.JSON(http.StatusAccepted, gin.H{"status": "deletion_scheduled", "deletionScheduledFor": at})
	})

	// Data export (FR-16): the caller's own data as a JSON download. Never cached.
	g.GET("/account/export", exportLimit, func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Content-Type", "application/json; charset=utf-8")
		h.Set("Content-Disposition", `attachment; filename="returnit-export-`+time.Now().UTC().Format("2006-01-02")+`.json"`)
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		if err := svc.Export(c.Request.Context(), userID(c), c.Writer); err != nil {
			if !c.Writer.Written() {
				// Nothing sent yet, so a normal error response is still possible.
				h.Del("Content-Disposition")
				writeAccountErr(c, err)
				return
			}
			// Mid-stream failure: the document is left unterminated (invalid JSON) on purpose.
			_ = c.Error(err)
			return
		}
		logger(c).Info("account data exported", "user_id", userID(c))
	})

	g.POST("/account/deletion/cancel", func(c *gin.Context) {
		if err := svc.CancelDeletion(c.Request.Context(), userID(c)); err != nil {
			writeAccountErr(c, err)
			return
		}
		logger(c).Info("account deletion cancelled", "user_id", userID(c))
		c.Status(http.StatusNoContent)
	})
}

func writeAccountErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, account.ErrNotFound):
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
	case errors.Is(err, account.ErrTooLate):
		c.JSON(http.StatusConflict, ErrorBody("deletion_in_progress", "the grace period has ended; the account can no longer be restored", c))
	default:
		internalError(c, err)
	}
}
