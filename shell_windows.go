//go:build windows

package main

import (
	"context"
	"os/exec"
)

func shellCommand(ctx context.Context, script string) *exec.Cmd {
	if shell, err := exec.LookPath("sh"); err == nil {
		return exec.CommandContext(ctx, shell, "-c", script)
	}
	return exec.CommandContext(ctx, "cmd", "/C", script)
}
