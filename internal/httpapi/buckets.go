package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/buckets"
)

type BucketService interface {
	List(ctx context.Context, userID uuid.UUID) ([]buckets.Bucket, error)
	Get(ctx context.Context, userID, id uuid.UUID) (buckets.Bucket, error)
	Create(ctx context.Context, userID uuid.UUID, name string) (buckets.Bucket, error)
	Rename(ctx context.Context, userID, id uuid.UUID, name string) (buckets.Bucket, error)
	Delete(ctx context.Context, userID, id uuid.UUID, confirmName string) error
	Members(ctx context.Context, userID, id uuid.UUID) ([]buckets.Member, error)
	RemoveMember(ctx context.Context, actor, id, target uuid.UUID) error
	TransferOwner(ctx context.Context, actor, id, newOwner uuid.UUID) error
	SetNotify(ctx context.Context, userID, id uuid.UUID, notify bool) (buckets.Bucket, error)
	CreateInvite(ctx context.Context, userID, id uuid.UUID) (buckets.Invite, error)
	RevokeInvite(ctx context.Context, userID, id uuid.UUID) error
	Preview(ctx context.Context, token string) (buckets.InvitePreview, error)
	Join(ctx context.Context, userID uuid.UUID, token string) (buckets.Bucket, error)
}

type bucketNameBody struct {
	Name string `json:"name"`
}

func registerBuckets(g *gin.RouterGroup, svc BucketService, inviteLimit gin.HandlerFunc) {
	if svc == nil {
		return
	}
	// bind parses a small JSON body; false means the 400 is already written.
	bind := func(c *gin.Context, v any) bool {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		if err := c.ShouldBindJSON(v); err != nil {
			c.JSON(http.StatusBadRequest, ErrorBody("bad_request", "invalid JSON body", c))
			return false
		}
		return true
	}
	// bucketID reads the :id path parameter; a malformed id looks like any other missing bucket.
	bucketID := func(c *gin.Context) (uuid.UUID, bool) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
			return uuid.Nil, false
		}
		return id, true
	}

	g.GET("/buckets", func(c *gin.Context) {
		out, err := svc.List(c.Request.Context(), userID(c))
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": out})
	})
	g.POST("/buckets", func(c *gin.Context) {
		var in bucketNameBody
		if !bind(c, &in) {
			return
		}
		b, err := svc.Create(c.Request.Context(), userID(c), in.Name)
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, b)
	})
	g.GET("/buckets/:id", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		b, err := svc.Get(c.Request.Context(), userID(c), id)
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.JSON(http.StatusOK, b)
	})
	g.PATCH("/buckets/:id", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		var in bucketNameBody
		if !bind(c, &in) {
			return
		}
		b, err := svc.Rename(c.Request.Context(), userID(c), id, in.Name)
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.JSON(http.StatusOK, b)
	})
	// Deleting a bucket deletes its purchases and receipts, so the body must repeat the bucket's exact name.
	g.DELETE("/buckets/:id", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		var in struct {
			ConfirmName string `json:"confirmName"`
		}
		if !bind(c, &in) {
			return
		}
		if err := svc.Delete(c.Request.Context(), userID(c), id, in.ConfirmName); err != nil {
			writeBucketErr(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	g.GET("/buckets/:id/members", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		out, err := svc.Members(c.Request.Context(), userID(c), id)
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": out})
	})
	// A person removes themselves (leaving); the owner removes anyone else.
	g.DELETE("/buckets/:id/members/:uid", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		target, err := uuid.Parse(c.Param("uid"))
		if err != nil {
			c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
			return
		}
		if err := svc.RemoveMember(c.Request.Context(), userID(c), id, target); err != nil {
			writeBucketErr(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	g.PUT("/buckets/:id/owner", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		var in struct {
			UserID uuid.UUID `json:"userId"`
		}
		if !bind(c, &in) {
			return
		}
		if err := svc.TransferOwner(c.Request.Context(), userID(c), id, in.UserID); err != nil {
			writeBucketErr(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	g.PUT("/buckets/:id/notify", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		var in struct {
			Notify *bool `json:"notify"`
		}
		if !bind(c, &in) {
			return
		}
		if in.Notify == nil {
			c.JSON(http.StatusBadRequest, ErrorBody("validation_error", "notify is required", c))
			return
		}
		b, err := svc.SetNotify(c.Request.Context(), userID(c), id, *in.Notify)
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.JSON(http.StatusOK, b)
	})
	// The token is returned once and is never logged (the key "token" is redacted); only its hash is stored.
	g.POST("/buckets/:id/invites", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		inv, err := svc.CreateInvite(c.Request.Context(), userID(c), id)
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusCreated, inv)
	})
	g.DELETE("/buckets/:id/invites", func(c *gin.Context) {
		id, ok := bucketID(c)
		if !ok {
			return
		}
		if err := svc.RevokeInvite(c.Request.Context(), userID(c), id); err != nil {
			writeBucketErr(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// Looking an invite up and using it are limited per person: the token is the only thing that grants entry.
	type tokenBody struct {
		Token string `json:"token"`
	}
	g.POST("/invites/preview", inviteLimit, func(c *gin.Context) {
		var in tokenBody
		if !bind(c, &in) {
			return
		}
		p, err := svc.Preview(c.Request.Context(), in.Token)
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, p)
	})
	g.POST("/invites/join", inviteLimit, func(c *gin.Context) {
		var in tokenBody
		if !bind(c, &in) {
			return
		}
		b, err := svc.Join(c.Request.Context(), userID(c), in.Token)
		if err != nil {
			writeBucketErr(c, err)
			return
		}
		c.JSON(http.StatusOK, b)
	})
}

func writeBucketErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, buckets.ErrNotFound):
		c.JSON(http.StatusNotFound, ErrorBody("not_found", "resource not found", c))
	case errors.Is(err, buckets.ErrNotOwner):
		c.JSON(http.StatusForbidden, ErrorBody("not_owner", "only the owner of the bucket can do this", c))
	case errors.Is(err, buckets.ErrValidation):
		c.JSON(http.StatusBadRequest, ErrorBody("validation_error", err.Error(), c))
	case errors.Is(err, buckets.ErrConflict):
		c.JSON(http.StatusConflict, ErrorBody("conflict", err.Error(), c))
	case errors.Is(err, buckets.ErrInviteGone):
		c.JSON(http.StatusGone, ErrorBody("invite_gone", "this invite is no longer valid", c))
	default:
		internalError(c, err)
	}
}
