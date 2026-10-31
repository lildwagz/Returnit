package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/malware"
	"github.com/returnit/returnit/internal/receipts"
)

type ReceiptService interface {
	MaxBytes() int64
	Upload(ctx context.Context, userID, purchaseID uuid.UUID, filename string, declaredSize int64, body io.Reader) (receipts.Receipt, error)
	SignedURL(ctx context.Context, userID, purchaseID uuid.UUID) (string, time.Time, receipts.Receipt, error)
	Delete(ctx context.Context, userID, purchaseID uuid.UUID) error
	Add(ctx context.Context, userID, purchaseID uuid.UUID, filename string, declaredSize int64, body io.Reader) (receipts.Receipt, error)
	SignedURLByID(ctx context.Context, userID, purchaseID, id uuid.UUID) (string, time.Time, receipts.Receipt, error)
	DeleteByID(ctx context.Context, userID, purchaseID, id uuid.UUID) error
}

// A purchase holds up to receipts.MaxPerPurchase files.
//   - /purchases/:id/receipt (singular) is the original API: upload replaces the oldest receipt (or adds the first),
//     download and delete act on the oldest one. Apps built before several receipts keep working.
//   - /purchases/:id/receipts adds one more; /purchases/:id/receipts/:rid downloads or deletes one named receipt.
func registerReceipts(g *gin.RouterGroup, svc ReceiptService, uploadLimit gin.HandlerFunc) {
	h := receiptHandler{svc}
	g.POST("/purchases/:id/receipt", uploadLimit, h.upload)
	g.GET("/purchases/:id/receipt", h.download)
	g.DELETE("/purchases/:id/receipt", h.delete)
	g.POST("/purchases/:id/receipts", uploadLimit, h.add)
	g.GET("/purchases/:id/receipts/:rid", h.download)
	g.DELETE("/purchases/:id/receipts/:rid", h.delete)
}

// receiptID reads the optional :rid. ok is false (and 404 sent) for a malformed one, like pathID.
func receiptID(c *gin.Context) (id *uuid.UUID, ok bool) {
	raw := c.Param("rid")
	if raw == "" {
		return nil, true
	}
	v, err := uuid.Parse(raw)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
		return nil, false
	}
	return &v, true
}

type receiptHandler struct{ svc ReceiptService }

// multipartOverhead leaves room for multipart boundaries and the filename header.
const multipartOverhead = 64 << 10

// upload is the original single-receipt upload (replace the oldest); add stores one more.
func (h receiptHandler) upload(c *gin.Context) { h.store(c, h.svc.Upload) }
func (h receiptHandler) add(c *gin.Context)    { h.store(c, h.svc.Add) }

type storeFunc func(ctx context.Context, userID, purchaseID uuid.UUID, filename string, declaredSize int64, body io.Reader) (receipts.Receipt, error)

func (h receiptHandler) store(c *gin.Context, save storeFunc) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	// Hard-cap the request body so oversized uploads are cut off while reading.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.svc.MaxBytes()+multipartOverhead)
	fh, err := c.FormFile("file")
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			c.JSON(http.StatusRequestEntityTooLarge, ErrorBody("file_too_large", "file exceeds the size limit", c))
			return
		}
		c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "multipart field 'file' is required", c))
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "cannot read file", c))
		return
	}
	defer f.Close()

	rec, err := save(c.Request.Context(), userID(c), id, fh.Filename, fh.Size, f)
	if err != nil {
		writeReceiptErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, rec)
}

func (h receiptHandler) download(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	rid, ok := receiptID(c)
	if !ok {
		return
	}
	var (
		url string
		exp time.Time
		rec receipts.Receipt
		err error
	)
	if rid == nil {
		url, exp, rec, err = h.svc.SignedURL(c.Request.Context(), userID(c), id)
	} else {
		url, exp, rec, err = h.svc.SignedURLByID(c.Request.Context(), userID(c), id, *rid)
	}
	if err != nil {
		writeReceiptErr(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"id": rec.ID, "url": url, "expiresAt": exp, "fileName": rec.FileName,
		"contentType": rec.ContentType, "sizeBytes": rec.SizeBytes,
	})
}

func (h receiptHandler) delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	rid, ok := receiptID(c)
	if !ok {
		return
	}
	var err error
	if rid == nil {
		err = h.svc.Delete(c.Request.Context(), userID(c), id)
	} else {
		err = h.svc.DeleteByID(c.Request.Context(), userID(c), id, *rid)
	}
	if err != nil {
		writeReceiptErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeReceiptErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, receipts.ErrNotFound):
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
	case errors.Is(err, receipts.ErrTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, ErrorBody("file_too_large", "file exceeds the size limit", c))
	case errors.Is(err, receipts.ErrUnsupported):
		c.JSON(http.StatusUnsupportedMediaType, ErrorBody("unsupported_media_type", "only PDF, JPEG and PNG are accepted", c))
	case errors.Is(err, malware.ErrInfected):
		c.JSON(http.StatusUnprocessableEntity, ErrorBody("malware_detected", "the file was rejected by the malware scanner", c))
	case errors.Is(err, malware.ErrUnavailable):
		c.Header("Retry-After", "30")
		c.JSON(http.StatusServiceUnavailable, ErrorBody("scan_unavailable", "file scanning is temporarily unavailable, try again later", c))
	case errors.Is(err, receipts.ErrLimit):
		c.JSON(http.StatusConflict, ErrorBody("receipt_limit_reached", "a purchase can have at most 5 receipts; delete one first", c))
	case errors.Is(err, receipts.ErrEmpty):
		c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "file is empty", c))
	default:
		internalError(c, err)
	}
}
