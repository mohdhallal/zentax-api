package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validBase() *Config {
	return &Config{
		App:      AppConfig{Env: "testing", Port: 3000},
		Database: DatabaseConfig{URL: "postgres://x"},
	}
}

func TestStorage_OmittedSectionDefaultsToFS(t *testing.T) {
	c := validBase()
	require.NoError(t, c.validate())
	assert.Equal(t, StorageDriverFS, c.Storage.Driver)
	assert.Equal(t, "./var/documents", c.Storage.FS.Root)
	assert.Equal(t, DefaultMaxUploadBytes, c.Storage.MaxUploadBytes)
}

func TestStorage_InvalidDriverFailsClosed(t *testing.T) {
	c := validBase()
	c.Storage.Driver = "gcs"
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.driver")
}

func TestStorage_S3NeedsBucketAndRegion(t *testing.T) {
	c := validBase()
	c.Storage.Driver = StorageDriverS3
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket")

	c.Storage.S3.Bucket = "b"
	err = c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region")

	c.Storage.S3.Region = "eu-west-1"
	require.NoError(t, c.validate())
}

func TestStorage_EnvOverrides(t *testing.T) {
	t.Setenv(EnvStorageDriver, "s3")
	t.Setenv(EnvStorageS3Bucket, "docs")
	t.Setenv(EnvStorageS3Region, "eu-central-1")
	t.Setenv(EnvStorageS3Endpoint, "http://minio:9000")
	t.Setenv(EnvStorageS3ForcePathStyle, "true")
	t.Setenv(EnvStorageMaxUploadBytes, "1024")
	t.Setenv(EnvStorageFSRoot, "/var/lib/zentax/documents")

	s := StorageConfig{Driver: "fs", FS: StorageFSConfig{Root: "./x"}, MaxUploadBytes: 5}
	mergeStorageEnvOverrides(&s)
	assert.Equal(t, "s3", s.Driver)
	assert.Equal(t, "docs", s.S3.Bucket)
	assert.Equal(t, "eu-central-1", s.S3.Region)
	assert.Equal(t, "http://minio:9000", s.S3.Endpoint)
	assert.True(t, s.S3.ForcePathStyle)
	assert.Equal(t, int64(1024), s.MaxUploadBytes)
	assert.Equal(t, "/var/lib/zentax/documents", s.FS.Root)
}

func TestStorage_EmptyEnvDoesNotOverride(t *testing.T) {
	// The compose file passes empty strings for the unused driver's settings.
	t.Setenv(EnvStorageDriver, "")
	t.Setenv(EnvStorageS3Bucket, "")
	t.Setenv(EnvStorageS3Region, "")
	t.Setenv(EnvStorageS3Endpoint, "")
	t.Setenv(EnvStorageS3ForcePathStyle, "")
	t.Setenv(EnvStorageMaxUploadBytes, "")
	t.Setenv(EnvStorageFSRoot, "")

	s := StorageConfig{
		Driver: "s3", MaxUploadBytes: 42,
		FS: StorageFSConfig{Root: "./keep"},
		S3: StorageS3Config{Bucket: "keep", Region: "keep", Endpoint: "keep", ForcePathStyle: true},
	}
	mergeStorageEnvOverrides(&s)
	assert.Equal(t, "s3", s.Driver)
	assert.Equal(t, int64(42), s.MaxUploadBytes)
	assert.Equal(t, "./keep", s.FS.Root)
	assert.Equal(t, StorageS3Config{Bucket: "keep", Region: "keep", Endpoint: "keep", ForcePathStyle: true}, s.S3)
}

func TestStorage_BadNumericEnvIgnored(t *testing.T) {
	t.Setenv(EnvStorageMaxUploadBytes, "lots")
	t.Setenv(EnvStorageS3ForcePathStyle, "maybe")
	s := StorageConfig{MaxUploadBytes: 7}
	mergeStorageEnvOverrides(&s)
	assert.Equal(t, int64(7), s.MaxUploadBytes)
	assert.False(t, s.S3.ForcePathStyle)
}
