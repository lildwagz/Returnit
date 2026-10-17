package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/devices"
)

type DeviceService interface {
	Register(ctx context.Context, userID uuid.UUID, in devices.Input) (devices.Device, error)
	Unregister(ctx context.Context, userID, deviceID uuid.UUID) error
}

func registerDevices(g *gin.RouterGroup, svc DeviceService) {
	// The app calls this at start-up and whenever the push token changes. The token is never
	// returned and never logged.
	g.POST("/devices", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		var in devices.Input
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "invalid JSON body", c))
			return
		}
		d, err := svc.Register(c.Request.Context(), userID(c), in)
		if err != nil {
			writeDeviceErr(c, err)
			return
		}
		c.JSON(http.StatusOK, d)
	})
	// Call on sign-out so the phone stops receiving this user's reminders.
	g.DELETE("/devices/:id", func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
			return
		}
		if err := svc.Unregister(c.Request.Context(), userID(c), id); err != nil {
			writeDeviceErr(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func writeDeviceErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, devices.ErrValidation):
		c.JSON(http.StatusBadRequest, ErrorBody("validation_error", err.Error(), c))
	case errors.Is(err, devices.ErrNotFound):
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
	default:
		internalError(c, err)
	}
}
