package s3

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/platform/storage"
)

// TestS3_Integration runs against a real S3-compatible endpoint (MinIO in the
// Compose stack, or AWS). Skipped unless TEST_S3_ENDPOINT, TEST_S3_BUCKET,
// AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are set; TEST_S3_CREATE_BUCKET=1
// creates the bucket first. Region defaults to us-east-1 (TEST_S3_REGION).
func TestS3_Integration(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	bucket := os.Getenv("TEST_S3_BUCKET")
	if endpoint == "" || bucket == "" || os.Getenv("AWS_ACCESS_KEY_ID") == "" || os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		t.Skip("set TEST_S3_ENDPOINT, TEST_S3_BUCKET, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY to run")
	}
	region := os.Getenv("TEST_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	ctx := t.Context()

	s, err := New(ctx, Options{Bucket: bucket, Region: region, Endpoint: endpoint, ForcePathStyle: true})
	require.NoError(t, err)

	if os.Getenv("TEST_S3_CREATE_BUCKET") == "1" {
		_, err := s.client.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)})
		var owned *s3types.BucketAlreadyOwnedByYou
		var exists *s3types.BucketAlreadyExists
		if err != nil && !errors.As(err, &owned) && !errors.As(err, &exists) {
			require.NoError(t, err)
		}
	}

	key := "tenants/" + uuid.NewString() + "/documents/d1/v1"
	body := []byte("%PDF-1.4 integration")
	t.Cleanup(func() { _ = s.Delete(ctx, key) })

	require.NoError(t, s.Put(ctx, key, bytes.NewReader(body), int64(len(body)), "application/pdf"))

	rc, info, err := s.Get(ctx, key)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	rc.Close()
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, int64(len(body)), info.Size)
	assert.Equal(t, "application/pdf", info.ContentType)

	// Overwrite replaces content + type.
	require.NoError(t, s.Put(ctx, key, strings.NewReader("second"), 6, "text/plain"))
	rc, info, err = s.Get(ctx, key)
	require.NoError(t, err)
	got, _ = io.ReadAll(rc)
	rc.Close()
	assert.Equal(t, "second", string(got))
	assert.Equal(t, "text/plain", info.ContentType)

	// Delete is idempotent; a missing key is ErrNotFound.
	require.NoError(t, s.Delete(ctx, key))
	require.NoError(t, s.Delete(ctx, key))
	_, _, err = s.Get(ctx, key)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// Key validation happens before any network call.
	assert.ErrorIs(t, s.Put(ctx, "../x", strings.NewReader("x"), 1, "text/plain"), storage.ErrInvalidKey)
}

func TestS3_NewFailsClosed(t *testing.T) {
	t.Parallel()
	_, err := New(t.Context(), Options{Region: "us-east-1"})
	assert.Error(t, err, "bucket required")
	_, err = New(t.Context(), Options{Bucket: "b"})
	assert.Error(t, err, "region required")
}
