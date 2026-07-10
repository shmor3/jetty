package main

import (
	"fmt"
	"strings"
)

func validateLinuxCommand(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("empty command")
	}
	if strings.ContainsRune(cmd, '\x00') {
		return fmt.Errorf("command contains a NUL byte")
	}
	return nil
}
