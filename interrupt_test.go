package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain either runs the test suite or behaves as the yq child process used
// by the interrupt end-to-end tests, depending on YQ_CHILD_MODE.
func TestMain(m *testing.M) {
	if os.Getenv("YQ_CHILD_MODE") == "1" {
		os.Exit(interruptChild())
	}
	os.Exit(m.Run())
}

func interruptChild() int {
	if dir := os.Getenv("YQ_CHILD_DIR"); dir != "" {
		if err := os.Chdir(dir); err != nil {
			return 99
		}
	}
	args := strings.Split(os.Getenv("YQ_CHILD_ARGS"), "\x1f")
	return runWithArgs(args)
}

const (
	splitDocs    = 5000
	childTimeout = 15 * time.Second
)

func startInterruptChild(t *testing.T, dir string, args ...string) (*exec.Cmd, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Private temp directory: assertions must not see files created by other
	// parallel test binaries (createTempFile uses os.TempDir).
	privateTemp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(privateTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"YQ_CHILD_MODE=1",
		"YQ_CHILD_DIR="+dir,
		"YQ_CHILD_ARGS="+strings.Join(args, "\x1f"),
		// TMPDIR for Unix, TMP/TEMP for Windows GetTempPath.
		"TMPDIR="+privateTemp,
		"TMP="+privateTemp,
		"TEMP="+privateTemp,
	)
	configureChildSysProcAttr(cmd)
	// Files, not pipes: a blocked pipe writer would distort timing.
	outFile, err := os.Create(filepath.Join(dir, "child.out"))
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "child.err"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = outFile.Close()
		_ = errFile.Close()
		_ = cmd.Wait()
	})
	return cmd, privateTemp
}

func childTempFiles(tempDir string) ([]string, error) {
	return filepath.Glob(filepath.Join(tempDir, "temp*"))
}

func waitForSplitFiles(t *testing.T, dir string, atLeast int) {
	t.Helper()
	deadline := time.Now().Add(childTimeout)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.yml"))
		if len(matches)-1 >= atLeast { // minus the big.yml input
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("child did not create %d split files in time", atLeast)
}

func waitForChildExit(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(childTimeout):
		_ = cmd.Process.Kill()
		t.Fatal("child did not exit after interrupt")
	}
	return cmd.ProcessState.ExitCode()
}

func writeManyDocs(t *testing.T, path string, docs int) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < docs; i++ {
		b.WriteString("a: x\n")
		if i < docs-1 {
			b.WriteString("---\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

func signalAndExpect130(t *testing.T, cmd *exec.Cmd, dir string) {
	t.Helper()
	sendInterrupt(t, cmd.Process)
	code := waitForChildExit(t, cmd)
	if code != 130 {
		stderr, _ := os.ReadFile(filepath.Join(dir, "child.err"))
		t.Fatalf("exit code = %d, want 130; stderr: %s", code, string(stderr))
	}
	if stderr, _ := os.ReadFile(filepath.Join(dir, "child.err")); len(stderr) != 0 {
		t.Fatalf("interrupted child must keep stderr empty, got: %s", string(stderr))
	}
}

func snapshotTempDir(t *testing.T, tempDir string) map[string]bool {
	t.Helper()
	matches, _ := childTempFiles(tempDir)
	set := make(map[string]bool, len(matches))
	for _, m := range matches {
		set[m] = true
	}
	return set
}

func assertNoTempLeftovers(t *testing.T, before map[string]bool, tempDir string) {
	t.Helper()
	matches, _ := childTempFiles(tempDir)
	for _, m := range matches {
		if !before[m] {
			t.Fatalf("temporary file leaked after interrupt: %s", m)
		}
	}
}

func assertCompleteSplitPrefix(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var outputs []string
	for _, e := range entries {
		name := e.Name()
		if name != "big.yml" && strings.HasSuffix(name, ".yml") {
			outputs = append(outputs, name)
		}
	}
	if len(outputs) == 0 || len(outputs) >= splitDocs {
		t.Fatalf("expected a proper prefix of complete split files, got %d", len(outputs))
	}
	for i := 0; i < len(outputs); i++ {
		name := fmt.Sprintf("%d.yml", i)
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("missing/gapped split file %s: %v", name, err)
		}
		want := "a: x\n"
		if i > 0 {
			want = "---\na: x\n"
		}
		if string(content) != want {
			t.Fatalf("file %s has partial content: %q", name, string(content))
		}
	}
	return len(outputs)
}

// Split output stops at a document boundary: earlier complete files remain,
// later files are never created, process exits 130.
func TestSignalInterruptSplitOutput(t *testing.T) {
	dir := t.TempDir()
	writeManyDocs(t, filepath.Join(dir, "big.yml"), splitDocs)

	cmd, tempDir := startInterruptChild(t, dir, "e", "-s", "$index", "big.yml")
	before := snapshotTempDir(t, tempDir)
	waitForSplitFiles(t, dir, 3)
	time.Sleep(20 * time.Millisecond)
	signalAndExpect130(t, cmd, dir)
	assertNoTempLeftovers(t, before, tempDir)
	assertCompleteSplitPrefix(t, dir)
}

// Two rapid interrupts: the second hard-exits the process with 130 even while
// the first is still being handled.
func TestDoubleSignalHardExit(t *testing.T) {
	dir := t.TempDir()
	writeManyDocs(t, filepath.Join(dir, "big.yml"), splitDocs)

	cmd, _ := startInterruptChild(t, dir, "e", "-s", "$index", "big.yml")
	waitForSplitFiles(t, dir, 3)

	start := time.Now()
	sendInterrupt(t, cmd.Process)
	sendInterrupt(t, cmd.Process)
	code := waitForChildExit(t, cmd)
	if code != 130 {
		t.Fatalf("double signal exit code = %d, want 130", code)
	}
	// Hard exit must be prompt (bounded), not waiting for finalisation.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("double signal took %v, expected immediate hard exit", elapsed)
	}
	for _, m := range mustGlob(t, filepath.Join(dir, "*.yml")) {
		name := strings.TrimSuffix(filepath.Base(m), ".yml")
		if name == "big" {
			continue
		}
		if _, err := strconv.Atoi(name); err != nil {
			t.Fatalf("unexpected split file after double signal: %s", m)
		}
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return matches
}
