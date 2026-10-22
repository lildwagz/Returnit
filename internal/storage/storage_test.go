package storage

import (
	"context"
	"net/url"
	"testing"
	"time"
)

func signed(t *testing.T, cfg Config) *url.URL {
	t.Helper()
	// newClient does not touch the network, and neither does presigning once the region is fixed.
	c, err := newClient(cfg.Endpoint, cfg.UseSSL, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := &S3{c: c, signer: c, bucket: cfg.Bucket}
	if cfg.PublicEndpoint != "" {
		if s.signer, err = newClient(cfg.PublicEndpoint, cfg.PublicUseSSL, cfg); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := s.SignedGetURL(context.Background(), "u/p/receipt.png", "struk.png", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSignedLinkUsesPublicEndpointWhenSet(t *testing.T) {
	u := signed(t, Config{Endpoint: "minio:9000", AccessKey: "a", SecretKey: "b", Bucket: "receipts", Region: "us-east-1",
		PublicEndpoint: "files.example.com", PublicUseSSL: true})
	if u.Scheme != "https" || u.Host != "files.example.com" {
		t.Fatalf("link must point at the public address, got %s://%s", u.Scheme, u.Host)
	}
	if u.Path != "/receipts/u/p/receipt.png" {
		t.Fatalf("unexpected path %q", u.Path)
	}
	if u.Query().Get("X-Amz-Signature") == "" || u.Query().Get("X-Amz-Expires") != "300" {
		t.Fatalf("link is not a 5-minute signed link: %s", u.RawQuery)
	}
}

func TestSignedLinkFallsBackToEndpoint(t *testing.T) {
	u := signed(t, Config{Endpoint: "localhost:9000", AccessKey: "a", SecretKey: "b", Bucket: "receipts", Region: "us-east-1"})
	if u.Scheme != "http" || u.Host != "localhost:9000" {
		t.Fatalf("got %s://%s", u.Scheme, u.Host)
	}
}

func TestPublicEndpointNeedsRegion(t *testing.T) {
	_, err := NewS3(context.Background(), Config{Endpoint: "127.0.0.1:1", AccessKey: "a", SecretKey: "b", Bucket: "receipts",
		PublicEndpoint: "files.example.com"})
	if err == nil {
		t.Fatal("expected an error: without a region the signer would have to ask the server")
	}
}
