package main

import (
	"os"
	"testing"
)

func TestHideFile(t *testing.T) {
	// 1. Success case
	f, err := os.CreateTemp("", "hide-file-test")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	err = hideFile(f.Name())
	if err != nil {
		t.Errorf("expected hideFile to succeed, got %v", err)
	}

	// 2. File not found
	err = hideFile(f.Name() + "nonexistent")
	if err == nil {
		t.Error("expected hideFile to fail for nonexistent file")
	}

	// 3. UTF16 error
	err = hideFile("invalid\x00path")
	if err == nil {
		t.Error("expected hideFile to fail for path with null byte")
	}
}
