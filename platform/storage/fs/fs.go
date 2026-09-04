// Package fs is the filesystem adapter of platform/storage (ADR-0022): the
// self-host default and the test backend. Objects live under a root directory
// at <root>/<key>; each object's content type is kept in a sidecar
// "<key>.meta" file (a tiny JSON document) so the adapter needs no extended
// attributes, which are neither portable across filesystems nor preserved by
// every volume driver.
//
// Writes are atomic: the bytes go to a temp file in the object's directory and
// are renamed over the final path, so a concurrent reader sees either the old
// object or the new one — never a partial file. Files are created 0600 and
// directories 0700: the blob store is private to the service user.
package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mohamadhallal/zentax-api/platform/storage"
)

const metaSuffix = ".meta"

type meta struct {
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

// Storage is the filesystem-backed object store.
type Storage struct {
	root string
}

var _ storage.Storage = (*Storage)(nil)

// New returns an adapter rooted at root, creating the directory if needed.
func New(root string) (*Storage, error) {
	if root == "" {
		return nil, errors.New("fs storage: root directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("fs storage: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("fs storage: create root: %w", err)
	}
	return &Storage{root: abs}, nil
}

// Root returns the absolute root directory.
func (s *Storage) Root() string { return s.root }

func (s *Storage) path(key string) (string, error) {
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}

func (s *Storage) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	final, err := s.path(key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(final)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fs storage: create object dir: %w", err)
	}

	// Write the bytes to a temp file next to the final path, then rename.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(final)+".*.tmp")
	if err != nil {
		return fmt.Errorf("fs storage: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fs storage: chmod temp file: %w", err)
	}
	written, err := io.Copy(tmp, r)
	if err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fs storage: write object: %w", err)
	}
	if size >= 0 && written != size {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fs storage: short write: got %d bytes, want %d", written, size)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fs storage: sync object: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("fs storage: close temp file: %w", err)
	}

	// The sidecar is written first (also atomically) so a reader that finds the
	// object always finds its metadata too.
	if err := writeAtomic(final+metaSuffix, mustJSON(meta{ContentType: contentType, Size: written})); err != nil {
		cleanup()
		return fmt.Errorf("fs storage: write metadata: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		cleanup()
		return fmt.Errorf("fs storage: commit object: %w", err)
	}
	return nil
}

func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	final, err := s.path(key)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	f, err := os.Open(final)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, storage.ObjectInfo{}, storage.ErrNotFound
		}
		return nil, storage.ObjectInfo{}, fmt.Errorf("fs storage: open object: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, storage.ObjectInfo{}, fmt.Errorf("fs storage: stat object: %w", err)
	}
	info := storage.ObjectInfo{Size: st.Size(), ContentType: "application/octet-stream"}
	if raw, err := os.ReadFile(final + metaSuffix); err == nil {
		var m meta
		if json.Unmarshal(raw, &m) == nil && m.ContentType != "" {
			info.ContentType = m.ContentType
		}
	}
	return f, info, nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	final, err := s.path(key)
	if err != nil {
		return err
	}
	for _, p := range []string{final, final + metaSuffix} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("fs storage: delete object: %w", err)
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
