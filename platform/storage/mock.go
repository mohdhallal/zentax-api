package storage

import (
	"context"
	"io"

	"github.com/stretchr/testify/mock"
)

// Mock is a testify mock of Storage for use-case unit tests.
type Mock struct {
	mock.Mock
}

var _ Storage = (*Mock)(nil)

func (m *Mock) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	// Drain the reader so callers computing a digest while streaming behave
	// exactly as they would against a real backend.
	if r != nil {
		_, _ = io.Copy(io.Discard, r)
	}
	args := m.Called(ctx, key, size, contentType)
	return args.Error(0)
}

func (m *Mock) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	args := m.Called(ctx, key)
	var rc io.ReadCloser
	if v := args.Get(0); v != nil {
		rc = v.(io.ReadCloser)
	}
	var info ObjectInfo
	if v := args.Get(1); v != nil {
		info = v.(ObjectInfo)
	}
	return rc, info, args.Error(2)
}

func (m *Mock) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}
