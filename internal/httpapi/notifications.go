package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/notifications"
)

type NotificationService interface {
	List(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit int) ([]notifications.Notification, error)
	MarkRead(ctx context.Context, userID, id uuid.UUID) error
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
}
