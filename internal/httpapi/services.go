package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/servicelog"
)

type ServiceLogService interface {
	List(ctx context.Context, userID, purchaseID uuid.UUID) ([]servicelog.Record, error)
	Create(ctx context.Context, userID, purchaseID uuid.UUID, raw servicelog.RawInput) (servicelog.Record, error)
	Update(ctx context.Context, userID, purchaseID, id uuid.UUID, raw servicelog.RawInput) (servicelog.Record, error)
	Delete(ctx context.Context, userID, purchaseID, id uuid.UUID) error
}

// The service history of a purchase: visits to a service centre and warranty claims. Anyone who can see the purchase
// can read and change it. nil leaves the routes out.
func registerServiceLog(g *gin.RouterGroup, svc ServiceLogService) {
	if svc == nil {
		return
	}
	h := serviceLogHandler{svc}
	g.GET("/purchases/:id/services", h.list)
	g.POST("/purchases/:id/services", h.create)
	g.PATCH("/purchases/:id/services/:sid", h.update)
	g.DELETE("/purchases/:id/services/:sid", h.delete)
}

type serviceLogHandler struct{ svc ServiceLogService }

// serviceID reads :sid; a malformed one is a 404 like any missing record.
func serviceID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("sid"))
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
		return uuid.Nil, false
	}
	return id, true
}

func bindServiceInput(c *gin.Context) (servicelog.RawInput, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	var raw servicelog.RawInput
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "invalid JSON body", c))
		return raw, false
	}
	return raw, true
}

func (h serviceLogHandler) list(c *gin.Context) {
	pid, ok := pathID(c)
	if !ok {
		return
	}
	recs, err := h.svc.List(c.Request.Context(), userID(c), pid)
	if err != nil {
		writeServiceLogErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": recs})
}

func (h serviceLogHandler) create(c *gin.Context) {
	pid, ok := pathID(c)
	if !ok {
		return
	}
	raw, ok := bindServiceInput(c)
	if !ok {
		return
	}
	rec, err := h.svc.Create(c.Request.Context(), userID(c), pid, raw)
	if err != nil {
		writeServiceLogErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, rec)
}

func (h serviceLogHandler) update(c *gin.Context) {
	pid, ok := pathID(c)
	if !ok {
		return
	}
	sid, ok := serviceID(c)
	if !ok {
		return
	}
	raw, ok := bindServiceInput(c)
	if !ok {
		return
	}
	rec, err := h.svc.Update(c.Request.Context(), userID(c), pid, sid, raw)
	if err != nil {
		writeServiceLogErr(c, err)
		return
	}
	c.JSON(http.StatusOK, rec)
}

func (h serviceLogHandler) delete(c *gin.Context) {
	pid, ok := pathID(c)
	if !ok {
		return
	}
	sid, ok := serviceID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), userID(c), pid, sid); err != nil {
		writeServiceLogErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeServiceLogErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, servicelog.ErrValidation):
		c.JSON(http.StatusBadRequest, ErrorBody("validation_error", err.Error(), c))
	case errors.Is(err, servicelog.ErrNotFound):
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
	case errors.Is(err, servicelog.ErrLimit):
		c.JSON(http.StatusConflict, ErrorBody("service_limit_reached", "a purchase can have at most 100 service records", c))
	default:
		internalError(c, err)
	}
}
