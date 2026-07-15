//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"time"
)

func shellCommand(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd", "/C", script)
	if shell, err := exec.LookPath("sh"); err == nil {
		cmd = exec.CommandContext(ctx, shell, "-c", script)
	}

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
