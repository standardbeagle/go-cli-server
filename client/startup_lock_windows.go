//go:build windows

package client

import "os"

func lockStartupFile(f *os.File) error {
	return nil
}

func unlockStartupFile(f *os.File) error {
	return nil
}
