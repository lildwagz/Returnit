package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/reminders"
)

type ReminderService interface {
	Get(ctx context.Context, userID uuid.UUID) (reminders.Preferences, error)
	Replace(ctx context.Context, userID uuid.UUID, in reminders.Input) (reminders.Preferences, error)
}

func registerReminders(g *gin.RouterGroup, svc ReminderService) {
	g.GET("/reminder-preferences", func(c *gin.Context) {
		p, err := svc.Get(c.Request.Context(), userID(c))
		if err != nil {
			writeReminderErr(c, err)
			return
		}
		c.JSON(http.StatusOK, p)
	})
	g.PUT("/reminder-preferences", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		var in reminders.Input
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "invalid JSON body", c))
			return
		}
		p, err := svc.Replace(c.Request.Context(), userID(c), in)
		if err != nil {
			writeReminderErr(c, err)
			return
		}
		c.JSON(http.StatusOK, p)
	})
}

func writeReminderErr(c *gin.Context, err error) {
	if errors.Is(err, reminders.ErrValidation) {
		c.JSON(http.StatusBadRequest, ErrorBody("validation_error", err.Error(), c))
		return
	}
	internalError(c, err)
}
