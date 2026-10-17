// Package storage abstracts private S3-compatible object storage (MinIO locally).
package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Store is the subset of object storage the application needs.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Delete(ctx context.Context, key string) error
	// SignedGetURL returns a short-lived download URL. Callers must check ownership first.
	SignedGetURL(ctx context.Context, key, downloadName string, ttl time.Duration) (string, error)
	// List calls fn for every object under prefix; fn returning an error stops the walk.
	List(ctx context.Context, prefix string, fn func(key string, modified time.Time) error) error
}

type Config struct {
	Endpoint  string // host:port, no scheme
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

type S3 struct {
	c      *minio.Client
	bucket string
}

// NewS3 connects and makes sure the (private) bucket exists.
func NewS3(ctx context.Context, cfg Config) (*S3, error) {
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: client: %w", err)
	}
	ok, err := c.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("storage: check bucket: %w", err)
	}
	if !ok {
		if err := c.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("storage: make bucket: %w", err)
		}
	}
	return &S3{c: c, bucket: cfg.Bucket}, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.c.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("storage: put: %w", err)
	}
	return nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if err := s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: delete: %w", err)
	}
	return nil
}

func (s *S3) SignedGetURL(ctx context.Context, key, downloadName string, ttl time.Duration) (string, error) {
	q := url.Values{}
	q.Set("response-content-disposition", mimeAttachment(downloadName))
	u, err := s.c.PresignedGetObject(ctx, s.bucket, key, ttl, q)
	if err != nil {
		return "", fmt.Errorf("storage: presign: %w", err)
	}
	return u.String(), nil
}

func mimeAttachment(name string) string {
	return "attachment; filename*=UTF-8''" + url.PathEscape(name)
}

func (s *S3) List(ctx context.Context, prefix string, fn func(key string, modified time.Time) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for obj := range s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return fmt.Errorf("storage: list: %w", obj.Err)
		}
		if err := fn(obj.Key, obj.LastModified); err != nil {
			return err
		}
	}
	return nil
}
