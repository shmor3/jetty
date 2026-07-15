package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

func copyFile(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source file %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", dst, err)
	}
	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", dst, err)
	}
	defer destFile.Close()
	if _, err = io.Copy(destFile, sourceFile); err != nil {
		// Don't leave a truncated destination behind on a failed copy.
		destFile.Close()
		os.Remove(dst)
		return fmt.Errorf("failed to copy data from %s to %s: %w", src, dst, err)
	}
	if err := os.Chmod(dst, sourceInfo.Mode()); err != nil {
		return fmt.Errorf("failed to chmod %s: %w", dst, err)
	}
	return nil
}

// copySymlink recreates the symlink at src as a symlink at dst (preserving its
// target) rather than dereferencing it.
func copySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return fmt.Errorf("failed to read symlink %s: %w", src, err)
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to replace %s: %w", dst, err)
	}
	if err := os.Symlink(target, dst); err != nil {
		return fmt.Errorf("failed to create symlink %s: %w", dst, err)
	}
	return nil
}

func copyDir(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source directory %s: %w", src, err)
	}
	// Create the destination writable so children can be written even when the
	// source directory is read-only; its mode is restored after the copy.
	if err := os.MkdirAll(dst, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dst, err)
	}
	// MkdirAll is a no-op if dst already exists (e.g. a prior copy restored a
	// read-only source mode); force it writable so this copy can write children.
	if err := os.Chmod(dst, 0755); err != nil {
		return fmt.Errorf("failed to prepare destination directory %s: %w", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("failed to read source directory %s: %w", src, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			err = copySymlink(srcPath, dstPath)
		case entry.IsDir():
			err = copyDir(ctx, srcPath, dstPath)
		default:
			err = copyFile(ctx, srcPath, dstPath)
		}
		if err != nil {
			return fmt.Errorf("failed to copy %s to %s: %w", srcPath, dstPath, err)
		}
	}
	// Restore the source directory's permissions now that its children exist.
	if err := os.Chmod(dst, srcInfo.Mode()); err != nil {
		return fmt.Errorf("failed to chmod %s: %w", dst, err)
	}
	return nil
}
func appendToFile(filename, content string) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return fmt.Errorf("failed to create directory for append %s: %w", filename, err)
	}
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file for append %s: %w", filename, err)
	}
	defer f.Close()
	if _, err = f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write to file %s: %w", filename, err)
	}
	return nil
}

type lineWriter struct {
	label    string
	state    *BuildState
	buf      []byte
	mu       sync.Mutex
	detached bool
}

func (w *lineWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.detached {
		return len(p), nil
	}
	for _, b := range p {
		if b == '\n' {
			w.state.log("%s: %s", w.label, string(w.buf))
			w.buf = w.buf[:0]
		} else if b != '\r' {
			w.buf = append(w.buf, b)
		}
	}
	return len(p), nil
}

func (w *lineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.detached {
		return nil
	}
	if len(w.buf) > 0 {
		w.state.log("%s: %s", w.label, string(w.buf))
		w.buf = w.buf[:0]
	}
	return nil
}

// detach makes all subsequent Write/Close calls no-ops. It is used to sever an
// abandoned writer (e.g. a container exec goroutine that outlived its build)
// from the result channel so it cannot send after that channel is closed.
func (w *lineWriter) detach() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.detached = true
	w.buf = nil
}
