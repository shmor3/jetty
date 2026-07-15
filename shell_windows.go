//go:build windows

package main

import (
	"context"
	"os/exec"
	"time"
)

func shellCommand(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd", "/C", script)
	if shell, err := exec.LookPath("sh"); err == nil {
		cmd = exec.CommandContext(ctx, shell, "-c", script)
	}

	cmd.Cancel = func() error {
		// Windows cannot deliver os.Interrupt to another process; Signal would
		// only return a misleading "not supported by windows" error. Return nil
		// so os/exec falls through to WaitDelay's forced kill of the child.
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
