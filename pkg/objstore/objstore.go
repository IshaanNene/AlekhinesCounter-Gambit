// Package objstore is a thin S3-compatible object store for durable artifacts:
// PGN archives, analysis JSON, and opening books.
//
// S3-compatible on purpose. MinIO locally, AWS S3 or GCS in production — the
// code and the client are identical, and only the endpoint and credentials
// change. Nothing here is a source of truth: Postgres owns games and reports.
// This is the archive and the download surface, so a store outage degrades
// exports and history downloads, never live play.
package objstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Buckets. One per kind of artifact, so a lifecycle rule (expire, archive to
// cold storage) can differ per class without touching object keys.
const (
	BucketPGN      = "pgn"
	BucketAnalysis = "analysis"
	BucketBooks    = "books"
)

// Store wraps a MinIO/S3 client.
//
// It keeps two clients when the public endpoint differs from the internal one:
// `client` for reads/writes over the internal network, and `presignClient` to
// sign download URLs for the endpoint the browser actually reaches. A presigned
// URL's signature covers its host, so signing against "minio:9000" yields a URL
// no host-side browser can use — the classic split-horizon trap.
type Store struct {
	client        *minio.Client
	presignClient *minio.Client
}

// Config describes how to reach the object store.
type Config struct {
	Endpoint  string // internal host:port for reads/writes, no scheme
	AccessKey string
	SecretKey string
	UseSSL    bool
	// PublicEndpoint is where clients reach the store (e.g. through an ingress).
	// Empty means it is the same as Endpoint. Presigned URLs are signed for this.
	PublicEndpoint string
	PublicSSL      bool
	// Region pins the S3 region so the public client signs offline instead of
	// making a location call to an endpoint it cannot reach.
	Region string
}

// Dial connects and ensures the buckets exist. An empty endpoint returns
// (nil, nil): object storage is off, and every method degrades to a no-op, so
// the platform still plays and analyses games without it.
func Dial(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Endpoint == "" {
		return nil, nil
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("object store client: %w", err)
	}
	s := &Store{client: client, presignClient: client}
	if cfg.PublicEndpoint != "" && cfg.PublicEndpoint != cfg.Endpoint {
		pub, err := minio.New(cfg.PublicEndpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
			Secure: cfg.PublicSSL,
			// Pin the region so presigning signs offline. Without it the client
			// makes a live bucket-location call to the *public* endpoint, which
			// this process cannot reach (its localhost is not the browser's).
			Region: cfg.Region,
		})
		if err != nil {
			return nil, fmt.Errorf("object store public client: %w", err)
		}
		s.presignClient = pub
	}

	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, b := range []string{BucketPGN, BucketAnalysis, BucketBooks} {
		if err := s.ensureBucket(dctx, b); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Enabled reports whether a store is attached.
func (s *Store) Enabled() bool { return s != nil && s.client != nil }

func (s *Store) ensureBucket(ctx context.Context, bucket string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("check bucket %q: %w", bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		// A racing replica may have created it between our check and make.
		if exists2, _ := s.client.BucketExists(ctx, bucket); exists2 {
			return nil
		}
		return fmt.Errorf("create bucket %q: %w", bucket, err)
	}
	return nil
}

// Put stores an object.
func (s *Store) Put(ctx context.Context, bucket, key string, data []byte, contentType string) error {
	if !s.Enabled() {
		return nil
	}
	_, err := s.client.PutObject(ctx, bucket, key,
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put %s/%s: %w", bucket, key, err)
	}
	return nil
}

// Get retrieves an object.
func (s *Store) Get(ctx context.Context, bucket, key string) ([]byte, error) {
	if !s.Enabled() {
		return nil, errors.New("object store is disabled")
	}
	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s/%s: %w", bucket, key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("read %s/%s: %w", bucket, key, err)
	}
	return data, nil
}

// PresignedGet returns a time-limited download URL.
//
// Presigned rather than proxied: the gateway hands out a URL and the client
// downloads straight from the store, so a large PGN never streams through the
// API. The URL carries its own short-lived signature — no bucket is ever public.
func (s *Store) PresignedGet(ctx context.Context, bucket, key string, ttl time.Duration, downloadName string) (string, error) {
	if !s.Enabled() {
		return "", errors.New("object store is disabled")
	}
	reqParams := url.Values{}
	if downloadName != "" {
		// Make the browser save it under a sensible filename rather than the key.
		reqParams.Set("response-content-disposition",
			fmt.Sprintf("attachment; filename=%q", downloadName))
	}
	u, err := s.presignClient.PresignedGetObject(ctx, bucket, key, ttl, reqParams)
	if err != nil {
		return "", fmt.Errorf("presign %s/%s: %w", bucket, key, err)
	}
	return u.String(), nil
}

// Exists reports whether an object is present.
func (s *Store) Exists(ctx context.Context, bucket, key string) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	_, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, fmt.Errorf("stat %s/%s: %w", bucket, key, err)
	}
	return true, nil
}
