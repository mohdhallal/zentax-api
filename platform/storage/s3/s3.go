// Package s3 is the S3 adapter of platform/storage (ADR-0022): AWS S3 in the
// SaaS edition, or any S3-compatible store (MinIO, Ceph RGW, …) via a custom
// endpoint with path-style addressing. Credentials come from the SDK default
// chain (env, shared config, IAM role / IRSA / ECS task role), never from the
// application config. No SDK type escapes this package.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/mohamadhallal/zentax-api/platform/storage"
)

// Options configures the adapter.
type Options struct {
	Bucket string
	Region string
	// Endpoint overrides the S3 endpoint (MinIO / on-prem); empty = AWS.
	Endpoint string
	// ForcePathStyle addresses objects as <endpoint>/<bucket>/<key> instead of
	// virtual-host style — required by most S3-compatible stores.
	ForcePathStyle bool
}

// Storage is the S3-backed object store.
type Storage struct {
	client *awss3.Client
	bucket string
}

var _ storage.Storage = (*Storage)(nil)

// New builds the adapter. It fails closed on a missing bucket or region and
// resolves credentials through the SDK default chain.
func New(ctx context.Context, opts Options) (*Storage, error) {
	if opts.Bucket == "" {
		return nil, errors.New("s3 storage: bucket is required")
	}
	if opts.Region == "" {
		return nil, errors.New("s3 storage: region is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(opts.Region))
	if err != nil {
		return nil, fmt.Errorf("s3 storage: load aws config: %w", err)
	}
	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		if opts.Endpoint != "" {
			o.BaseEndpoint = aws.String(opts.Endpoint)
		}
		o.UsePathStyle = opts.ForcePathStyle
	})
	return &Storage{client: client, bucket: opts.Bucket}, nil
}

// NewWithClient wires an already-built client (tests, custom middleware).
func NewWithClient(client *awss3.Client, bucket string) *Storage {
	return &Storage{client: client, bucket: bucket}
}

func (s *Storage) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := storage.ValidateKey(key); err != nil {
		return err
	}
	input := &awss3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        r,
		ContentType: aws.String(contentType),
	}
	if size >= 0 {
		input.ContentLength = aws.Int64(size)
	}
	if _, err := s.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("s3 storage: put object: %w", err)
	}
	return nil
}

func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := storage.ValidateKey(key); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, storage.ObjectInfo{}, storage.ErrNotFound
		}
		return nil, storage.ObjectInfo{}, fmt.Errorf("s3 storage: get object: %w", err)
	}
	info := storage.ObjectInfo{ContentType: aws.ToString(out.ContentType)}
	if out.ContentLength != nil {
		info.Size = *out.ContentLength
	}
	if info.ContentType == "" {
		info.ContentType = "application/octet-stream"
	}
	return out.Body, info, nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := storage.ValidateKey(key); err != nil {
		return err
	}
	// S3 DeleteObject is idempotent (204 for a missing key); a NoSuchKey from
	// a strict compatible store is treated the same way.
	if _, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil && !isNotFound(err) {
		return fmt.Errorf("s3 storage: delete object: %w", err)
	}
	return nil
}

func isNotFound(err error) bool {
	var noKey *s3types.NoSuchKey
	if errors.As(err, &noKey) {
		return true
	}
	var notFound *s3types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	// Some compatible stores answer HeadObject/GetObject with a bare 404.
	msg := err.Error()
	return strings.Contains(msg, "StatusCode: 404") || strings.Contains(msg, "NoSuchKey")
}
