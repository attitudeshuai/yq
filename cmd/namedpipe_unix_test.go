//go:build !windows

package cmd

import (
	"os"
	"syscall"
	"testing"
)

func openNamedPipeForTest(t *testing.T, path string) *os.File {
	t.Helper()
	// O_RDWR avoids blocking on open: a FIFO opened only for reading
	// (or only writing) blocks until a peer opens the other end.
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("failed to create named pipe: %v", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, os.ModeNamedPipe)
	if err != nil {
		t.Fatalf("failed to open named pipe: %v", err)
	}
	return f
}
