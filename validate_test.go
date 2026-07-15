package main

import (
	"testing"
)

func TestValidateLinuxCommand(t *testing.T) {
	if err := validateLinuxCommand(""); err == nil {
		t.Error("expected error for empty command")
	}
	if err := validateLinuxCommand("   "); err == nil {
		t.Error("expected error for empty command")
	}
	if err := validateLinuxCommand("echo \x00hello"); err == nil {
		t.Error("expected error for NUL byte")
	}
	if err := validateLinuxCommand("echo hello"); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
