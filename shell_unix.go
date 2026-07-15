//go:build !windows

package main

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

func shellCommand(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Send SIGTERM to the entire process group to allow graceful shutdown
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	// Wait 5 seconds before Go forcibly sends SIGKILL to the main process
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
