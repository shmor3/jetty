package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	err := os.WriteFile(src, []byte("hello"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err = copyFile(ctx, src, dst)
	if err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read dst: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("expected hello, got %s", string(content))
	}
}

func TestCopyDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "srcdir")
	dst := filepath.Join(dir, "dstdir")

	err := os.Mkdir(src, 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(src, "file1.txt"), []byte("file1"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(filepath.Join(src, "subdir"), 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(src, "subdir", "file2.txt"), []byte("file2"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err = copyDir(ctx, src, dst)
	if err != nil {
		t.Fatalf("copyDir failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dst, "file1.txt"))
	if err != nil {
		t.Fatalf("failed to read file1: %v", err)
	}
	if string(content) != "file1" {
		t.Errorf("expected file1, got %s", string(content))
	}

	content, err = os.ReadFile(filepath.Join(dst, "subdir", "file2.txt"))
	if err != nil {
		t.Fatalf("failed to read file2: %v", err)
	}
	if string(content) != "file2" {
		t.Errorf("expected file2, got %s", string(content))
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err = copyDir(cancelCtx, src, filepath.Join(dir, "dst2"))
	if err == nil {
		t.Error("expected copyDir to fail after context cancel")
	}

	err = copyDir(context.Background(), filepath.Join(dir, "nonexistent"), dst)
	if err == nil {
		t.Error("expected copyDir to fail on nonexistent src")
	}

	// MkdirAll fails
	fileAsDir := filepath.Join(dir, "file_as_dir")
	os.WriteFile(fileAsDir, []byte("foo"), 0644)
	err = copyDir(context.Background(), src, filepath.Join(fileAsDir, "dir"))
	if err == nil {
		t.Error("expected copyDir to fail on MkdirAll error")
	}
}

func TestCopyFileEdgeCases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()

	src := filepath.Join(dir, "src.txt")
	os.WriteFile(src, []byte("hello"), 0644)

	cancel()
	err := copyFile(ctx, src, filepath.Join(dir, "dst2.txt"))
	if err == nil {
		t.Error("expected copyFile to fail after context cancel")
	}

	err = copyFile(context.Background(), filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dst.txt"))
	if err == nil {
		t.Error("expected copyFile to fail on nonexistent src")
	}

	// MkdirAll fails
	fileAsDir := filepath.Join(dir, "file_as_dir")
	os.WriteFile(fileAsDir, []byte("foo"), 0644)
	err = copyFile(context.Background(), src, filepath.Join(fileAsDir, "file.txt"))
	if err == nil {
		t.Error("expected copyFile to fail on MkdirAll error")
	}
}

func TestAppendToFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "sub", "file.txt")

	err := appendToFile(dst, "hello")
	if err != nil {
		t.Fatalf("appendToFile failed: %v", err)
	}
	err = appendToFile(dst, " world")
	if err != nil {
		t.Fatalf("appendToFile failed: %v", err)
	}
	content, _ := os.ReadFile(dst)
	if string(content) != "hello world" {
		t.Errorf("expected hello world, got %s", string(content))
	}

	// OpenFile fails
	err = appendToFile(dir, "fail")
	if err == nil {
		t.Error("expected error appending to directory")
	}

	// MkdirAll fails
	fileAsDir := filepath.Join(dir, "file_as_dir")
	os.WriteFile(fileAsDir, []byte("foo"), 0644)
	err = appendToFile(filepath.Join(fileAsDir, "file.txt"), "fail")
	if err == nil {
		t.Error("expected error creating directory when path is a file")
	}
}

func TestLineWriterClose(t *testing.T) {
	state := &BuildState{
		Context: context.Background(),
	}
	w := &lineWriter{
		label: "test",
		state: state,
		buf:   []byte("test"),
	}
	w.Close()
	if len(w.buf) != 0 {
		t.Errorf("expected buf to be empty, got %v", w.buf)
	}
}
