//go:build !windows && !plan9 && !js && !wasip1

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func configureChildSysProcAttr(_ *exec.Cmd) {}

func sendInterrupt(t *testing.T, p *os.Process) {
	t.Helper()
	if err := p.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}
}

// SIGTERM interrupts in-place editing: the target is unchanged and the temp
// file is rolled back.
func TestSignalTerminateInPlace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.yml")
	original := strings.Repeat("keep: me\n---\n", 400) + "keep: me\n"
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cmd, tempDir := startInterruptChild(t, dir, "e", "-i", ".", "f.yml")
	before := snapshotTempDir(t, tempDir)

	// Wait for the in-place temp file, then signal quickly.
	deadline := time.Now().Add(childTimeout)
	found := false
	for time.Now().Before(deadline) {
		matches, _ := childTempFiles(tempDir)
		for _, m := range matches {
			if !before[m] {
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !found {
		t.Fatal("in-place temp file never appeared")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	code := waitForChildExit(t, cmd)
	if code != 130 {
		stderr, _ := os.ReadFile(filepath.Join(dir, "child.err"))
		t.Fatalf("exit code = %d, want 130; stderr: %s", code, string(stderr))
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatal("target file changed during interrupted in-place write")
	}
	assertNoTempLeftovers(t, before, tempDir)
}
