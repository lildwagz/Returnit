package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/notifications"
)

type NotificationService interface {
	List(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit int) ([]notifications.Notification, error)
	MarkRead(ctx context.Context, userID, id uuid.UUID) error
	// Snooze repeats a delivered reminder later (choice: tomorrow, in_3_days, next_week) and returns the new time.
	Snooze(ctx context.Context, userID, id uuid.UUID, choice string) (time.Time, error)
}

func registerNotifications(g *gin.RouterGroup, svc NotificationService) {
	g.GET("/notifications", func(c *gin.Context) {
		unread := false
		if v := c.Query("unreadOnly"); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "unreadOnly must be true or false", c))
				return
			}
			unread = b
		}
		limit := 0
		if v := c.Query("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "limit must be a positive integer", c))
				return
			}
			limit = n
		}
		items, err := svc.List(c.Request.Context(), userID(c), unread, limit)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, items)
	})

	g.POST("/notifications/:id/read", func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		if err := svc.MarkRead(c.Request.Context(), userID(c), id); err != nil {
			if errors.Is(err, notifications.ErrNotFound) {
				c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
				return
			}
			internalError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	g.POST("/notifications/:id/snooze", func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<10)
		var body struct {
			Until string `json:"until"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "invalid JSON body", c))
			return
		}
		at, err := svc.Snooze(c.Request.Context(), userID(c), id, body.Until)
		switch {
		case errors.Is(err, notifications.ErrNotFound):
			c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
		case errors.Is(err, notifications.ErrBadSnooze):
			c.JSON(http.StatusBadRequest, ErrorBody("validation_error", "until must be tomorrow, in_3_days or next_week", c))
		case errors.Is(err, notifications.ErrSnoozeNotPossible):
			c.JSON(http.StatusUnprocessableEntity, ErrorBody("snooze_not_possible", "this reminder cannot be repeated later", c))
		case err != nil:
			internalError(c, err)
		default:
			c.JSON(http.StatusOK, gin.H{"scheduledAt": at.UTC()})
		}
	})
}
