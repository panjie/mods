// Package cache provides a simple in-file cache implementation.
package cache

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Type represents the type of cache being used.
type Type string

// Cache types for different purposes.
const (
	ConversationCache Type = "conversations"
	TemporaryCache    Type = "temp"
)

const cacheExt = ".gob"

var errInvalidID = errors.New("invalid id")

// Cache is a generic cache implementation that stores data in files.
type Cache[T any] struct {
	baseDir string
	cType   Type
}

// New creates a new cache instance with the specified base directory and cache type.
func New[T any](baseDir string, cacheType Type) (*Cache[T], error) {
	dir := filepath.Join(baseDir, string(cacheType))
	if err := os.MkdirAll(dir, os.ModePerm); err != nil { //nolint:gosec
		return nil, fmt.Errorf("create cache directory: %w", err)
	}
	return &Cache[T]{
		baseDir: baseDir,
		cType:   cacheType,
	}, nil
}

func (c *Cache[T]) dir() string {
	return filepath.Join(c.baseDir, string(c.cType))
}

func (c *Cache[T]) Read(id string, readFn func(io.Reader) error) error {
	if id == "" {
		return fmt.Errorf("read: %w", errInvalidID)
	}
	file, err := os.Open(filepath.Join(c.dir(), id+cacheExt))
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	defer file.Close() //nolint:errcheck

	if err := readFn(file); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}

func (c *Cache[T]) Write(id string, writeFn func(io.Writer) error) (err error) {
	if id == "" {
		return fmt.Errorf("write: %w", errInvalidID)
	}

	// Write to a sibling temp file, fsync, then atomically rename it over the
	// target. This guarantees a crash mid-write cannot corrupt or truncate an
	// existing valid cache entry (previously os.Create truncated the file to
	// zero before the new content was written).
	final := filepath.Join(c.dir(), id+cacheExt)
	tmp := final + ".tmp"

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	if err = writeFn(f); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	// Explicit close to surface deferred write errors (ENOSPC/EIO) before
	// committing the rename. The deferred Close above becomes a no-op double
	// close on this path.
	if err = f.Close(); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err = os.Rename(tmp, final); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// Delete removes a cached item by its ID.
func (c *Cache[T]) Delete(id string) error {
	if id == "" {
		return fmt.Errorf("delete: %w", errInvalidID)
	}
	if err := os.Remove(filepath.Join(c.dir(), id+cacheExt)); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}
