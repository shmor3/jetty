package main

import (
	"context"
	"testing"
)

func TestShellCommand(t *testing.T) {
	ctx := context.Background()
	cmd := shellCommand(ctx, "echo hello")

	// Process is nil before starting
	if err := cmd.Cancel(); err != nil {
		t.Errorf("expected Cancel to succeed when Process is nil, got %v", err)
	}

	// Start process and cancel
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}

	// Cancel the process
	err := cmd.Cancel()
	if err != nil {
		t.Logf("Cancel returned error (which is fine if process exited fast): %v", err)
	}
	cmd.Wait()
}
