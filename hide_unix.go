//go:build !windows

package main

// hideFile is a no-op on Unix systems since directories starting with a dot are natively hidden.
func hideFile(path string) error {
	return nil
}
