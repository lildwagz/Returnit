package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/notifications"
)

type LanguageService interface {
	Set(ctx context.Context, userID uuid.UUID, lang string) error
}

// registerLanguage lets the app tell the server which language the person uses, so reminders (push, e-mail and the
// inbox) are written in it. The app calls it after sign-in and whenever the language changes; repeating it is harmless.
func registerLanguage(g *gin.RouterGroup, svc LanguageService) {
	if svc == nil {
		return
	}
	g.PUT("/account/language", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		var in struct {
			Language string `json:"language"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "invalid JSON body", c))
			return
		}
		if err := svc.Set(c.Request.Context(), userID(c), in.Language); err != nil {
			if errors.Is(err, notifications.ErrLanguage) {
				c.JSON(http.StatusBadRequest, ErrorBody("validation_error", "language must be id, en or ar", c))
				return
			}
			internalError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}
