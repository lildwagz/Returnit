package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/purchases"
)

type PurchaseService interface {
	Create(ctx context.Context, userID uuid.UUID, raw purchases.RawInput) (purchases.Purchase, error)
	Get(ctx context.Context, userID, id uuid.UUID) (purchases.Purchase, error)
	List(ctx context.Context, userID uuid.UUID, f purchases.ListFilter) ([]purchases.Purchase, int, error)
	Update(ctx context.Context, userID, id uuid.UUID, raw purchases.RawInput) (purchases.Purchase, error)
	Delete(ctx context.Context, userID, id uuid.UUID) error
	Summary(ctx context.Context, userID uuid.UUID) (purchases.Summary, error)
	Deadlines(ctx context.Context, userID uuid.UUID, pastDays int) ([]purchases.Deadline, error)
}

const maxBodyBytes = 64 << 10

func registerPurchases(g *gin.RouterGroup, svc PurchaseService) {
	h := purchaseHandler{svc}
	// Not under /purchases/: that path takes an id, and a summary must never be mistaken for one.
	g.GET("/summary", h.summary)
	g.GET("/deadlines", h.deadlines)
	g.GET("/purchases", h.list)
	g.POST("/purchases", h.create)
	g.GET("/purchases/:id", h.get)
	g.PATCH("/purchases/:id", h.update)
	g.DELETE("/purchases/:id", h.delete)
}

type purchaseHandler struct{ svc PurchaseService }

func (h purchaseHandler) create(c *gin.Context) {
	raw, ok := bindInput(c)
	if !ok {
		return
	}
	p, err := h.svc.Create(c.Request.Context(), userID(c), raw)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (h purchaseHandler) get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	p, err := h.svc.Get(c.Request.Context(), userID(c), id)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h purchaseHandler) update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	raw, ok := bindInput(c)
	if !ok {
		return
	}
	p, err := h.svc.Update(c.Request.Context(), userID(c), id, raw)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h purchaseHandler) delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), userID(c), id); err != nil {
		writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h purchaseHandler) summary(c *gin.Context) {
	s, err := h.svc.Summary(c.Request.Context(), userID(c))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, s)
}

// deadlines feeds the Tenggat tab: warranty ends and return deadlines, from `pastDays` days ago (default 30) onward.
func (h purchaseHandler) deadlines(c *gin.Context) {
	past := purchases.DefaultPastDays
	if raw := c.Query("pastDays"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "pastDays must be an integer", c))
			return
		}
		past = n
	}
	items, err := h.svc.Deadlines(c.Request.Context(), userID(c), past)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h purchaseHandler) list(c *gin.Context) {
	page, err1 := intQuery(c, "page")
	size, err2 := intQuery(c, "pageSize")
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "page and pageSize must be integers", c))
		return
	}
	f := purchases.ListFilter{Query: c.Query("q"), Status: c.Query("warrantyStatus"), Category: c.Query("category"), Page: page, PageSize: size}
	items, total, err := h.svc.List(c.Request.Context(), userID(c), f)
	if err != nil {
		writeErr(c, err)
		return
	}
	// Echo the effective (clamped) paging values.
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = purchases.DefaultPageSize
	}
	if f.PageSize > purchases.MaxPageSize {
		f.PageSize = purchases.MaxPageSize
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": f.Page, "pageSize": f.PageSize, "total": total})
}

func intQuery(c *gin.Context, key string) (int, error) {
	v := c.Query(key)
	if v == "" {
		return 0, nil
	}
	return strconv.Atoi(v)
}

func bindInput(c *gin.Context) (purchases.RawInput, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	var raw purchases.RawInput
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "invalid JSON body", c))
		return raw, false
	}
	return raw, true
}

// pathID returns 404 for malformed IDs so callers cannot distinguish them from missing ones.
func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
		return uuid.Nil, false
	}
	return id, true
}

func writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, purchases.ErrValidation):
		c.JSON(http.StatusBadRequest, ErrorBody("validation_error", err.Error(), c))
	case errors.Is(err, purchases.ErrNotFound):
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
	default:
		internalError(c, err)
	}
}
