package fs

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/platform/storage"
)

func newStore(t *testing.T) *Storage {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "blobs"))
	require.NoError(t, err)
	return s
}

func TestFS_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	body := []byte("%PDF-1.4 hello")

	require.NoError(t, s.Put(ctx, "tenants/t1/documents/d1/v1", bytes.NewReader(body), int64(len(body)), "application/pdf"))

	rc, info, err := s.Get(ctx, "tenants/t1/documents/d1/v1")
	require.NoError(t, err)
	defer rc.Close()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, int64(len(body)), info.Size)
	assert.Equal(t, "application/pdf", info.ContentType, "content type round-trips via the sidecar")

	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(s.Root(), "tenants/t1/documents/d1/v1"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "objects are private to the service user")
	}
	// No temp files linger.
	entries, err := os.ReadDir(filepath.Join(s.Root(), "tenants/t1/documents/d1"))
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), ".tmp"), "temp file left behind: %s", e.Name())
	}
}

func TestFS_NotFound(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	_, _, err := s.Get(t.Context(), "tenants/t1/documents/missing/v1")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestFS_DeleteIdempotent(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.Put(ctx, "a/b", strings.NewReader("x"), 1, "text/plain"))
	require.NoError(t, s.Delete(ctx, "a/b"))
	_, _, err := s.Get(ctx, "a/b")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	require.NoError(t, s.Delete(ctx, "a/b"), "second delete is a no-op")
	require.NoError(t, s.Delete(ctx, "never/existed"))
	_, err = os.Stat(filepath.Join(s.Root(), "a/b.meta"))
	assert.True(t, os.IsNotExist(err), "sidecar removed with the object")
}

func TestFS_AtomicOverwrite(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.Put(ctx, "k", strings.NewReader("first"), 5, "text/plain"))
	require.NoError(t, s.Put(ctx, "k", strings.NewReader("second!"), 7, "text/csv"))

	rc, info, err := s.Get(ctx, "k")
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	assert.Equal(t, "second!", string(got))
	assert.Equal(t, int64(7), info.Size)
	assert.Equal(t, "text/csv", info.ContentType)
}

func TestFS_ShortWriteRejected(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	err := s.Put(t.Context(), "k", strings.NewReader("abc"), 10, "text/plain")
	require.Error(t, err)
	_, _, err = s.Get(t.Context(), "k")
	assert.ErrorIs(t, err, storage.ErrNotFound, "a failed put leaves no object behind")
}

func TestFS_KeyValidation(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	bad := []string{
		"", "/abs", "../escape", "a/../b", "a/./b", "a//b", "a/", "a b", "a\\b", "ü", "a?b", "a/..",
	}
	for _, k := range bad {
		assert.ErrorIs(t, s.Put(ctx, k, strings.NewReader("x"), 1, "text/plain"), storage.ErrInvalidKey, "put %q", k)
		_, _, err := s.Get(ctx, k)
		assert.ErrorIs(t, err, storage.ErrInvalidKey, "get %q", k)
		assert.ErrorIs(t, s.Delete(ctx, k), storage.ErrInvalidKey, "delete %q", k)
	}
	// Nothing escaped the root.
	outside := filepath.Join(filepath.Dir(s.Root()), "escape")
	_, err := os.Stat(outside)
	assert.True(t, os.IsNotExist(err))

	// (a key that is a prefix directory of another — "a" and "a/b" — cannot
	// coexist on a filesystem; application keys always share one depth.)
	good := []string{"a", "b/c", "tenants/T-1/documents/d_1/v.1", "0/1/2"}
	for _, k := range good {
		require.NoError(t, s.Put(ctx, k, strings.NewReader("x"), 1, "text/plain"), "put %q", k)
	}
}

func TestFS_MissingSidecarFallsBackToOctetStream(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.Put(ctx, "k", strings.NewReader("x"), 1, "text/plain"))
	require.NoError(t, os.Remove(filepath.Join(s.Root(), "k.meta")))
	rc, info, err := s.Get(ctx, "k")
	require.NoError(t, err)
	rc.Close()
	assert.Equal(t, "application/octet-stream", info.ContentType)
}

func TestFS_NewRequiresRoot(t *testing.T) {
	t.Parallel()
	_, err := New("")
	assert.Error(t, err)
}

func TestValidateKey(t *testing.T) {
	t.Parallel()
	assert.NoError(t, storage.ValidateKey("tenants/x/documents/y/z"))
	assert.ErrorIs(t, storage.ValidateKey("../x"), storage.ErrInvalidKey)
	assert.ErrorIs(t, storage.ValidateKey("/x"), storage.ErrInvalidKey)
	assert.ErrorIs(t, storage.ValidateKey("x/../../y"), storage.ErrInvalidKey)
}
