// Package storage is the provider-agnostic object-storage seam (ADR-0009
// adapter #1, ADR-0022): the documents module speaks only this interface, and
// the concrete adapters (fs, s3) live in their own packages so no cloud-SDK
// type ever leaves an adapter.
//
// Keys are opaque, slash-separated paths chosen by the application (never by a
// user): `tenants/<tenant>/documents/<document>/<version>`. Every adapter
// validates them with ValidateKey before touching the backend.
package storage

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
)

// ErrNotFound is returned by Get when no object exists under the key.
var ErrNotFound = errors.New("storage: object not found")

// ErrInvalidKey is returned when a key fails ValidateKey.
var ErrInvalidKey = errors.New("storage: invalid object key")

// ObjectInfo is the metadata an adapter returns alongside an object's bytes.
type ObjectInfo struct {
	Size        int64
	ContentType string
}

// Storage is the object store contract.
type Storage interface {
	// Put stores size bytes read from r under key with the given content type,
	// replacing any existing object atomically (a reader never observes a
	// partial write).
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens the object under key. The caller must Close the reader. A
	// missing object is reported as ErrNotFound.
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	// Delete removes the object under key. Idempotent: a missing object is not
	// an error.
	Delete(ctx context.Context, key string) error
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9/_.-]+$`)

// ValidateKey enforces the key grammar shared by every adapter: the character
// class [A-Za-z0-9/_.-], no leading "/", no empty segment and no "." / ".."
// segment — so a key can never escape a root directory or a bucket prefix.
func ValidateKey(key string) error {
	if key == "" || !keyPattern.MatchString(key) || strings.HasPrefix(key, "/") {
		return ErrInvalidKey
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return ErrInvalidKey
		}
	}
	return nil
}
