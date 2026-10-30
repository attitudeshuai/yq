//go:build plan9 || js || wasip1

package main

import (
	"os"
	"os/exec"
	"testing"
)

func configureChildSysProcAttr(_ *exec.Cmd) {}

func sendInterrupt(t *testing.T, _ *os.Process) {
	t.Helper()
	t.Skip("interrupt signals are not supported on this platform")
}
