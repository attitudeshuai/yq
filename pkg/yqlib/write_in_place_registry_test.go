package yqlib

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Active-run behaviour of the write in place handler: transitions only flip
// registry state; Finalise performs the work.
func TestWriteInPlaceActiveRunCommitAndRollback(t *testing.T) {
	t.Run("commit renames temp over target", func(t *testing.T) {
		run := newTestRun(t)
		target := filepath.Join(t.TempDir(), "input.yml")
		if err := os.WriteFile(target, []byte("old: 1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		handler := NewWriteInPlaceHandler(target)
		temp, err := handler.CreateTempFile()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := temp.WriteString("new: 2\n"); err != nil {
			t.Fatal(err)
		}

		// Before finalise the target is untouched.
		content, err := os.ReadFile(target)
		if err != nil || string(content) != "old: 1\n" {
			t.Fatalf("target changed before finalise: %q %v", content, err)
		}

		if err := handler.FinishWriteInPlace(true); err != nil {
			t.Fatalf("finish should be a state transition only: %v", err)
		}
		content, _ = os.ReadFile(target)
		if string(content) != "old: 1\n" {
			t.Fatalf("target changed before Finalise ran: %q", content)
		}

		interrupted, err := run.Finalise(true)
		if err != nil || interrupted {
			t.Fatalf("finalise: err=%v interrupted=%v", err, interrupted)
		}
		content, err = os.ReadFile(target)
		if err != nil || string(content) != "new: 2\n" {
			t.Fatalf("target not updated: %q %v", content, err)
		}
	})

	t.Run("rollback removes temp and keeps target", func(t *testing.T) {
		run := newTestRun(t)
		target := filepath.Join(t.TempDir(), "input.yml")
		if err := os.WriteFile(target, []byte("old: 1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		handler := NewWriteInPlaceHandler(target)
		temp, err := handler.CreateTempFile()
		if err != nil {
			t.Fatal(err)
		}
		tempPath := temp.Name()
		if _, err := temp.WriteString("new: 2\n"); err != nil {
			t.Fatal(err)
		}
		handler.FinishWriteInPlace(false)
		if _, err := run.Finalise(false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(tempPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("temp file should be removed after rollback")
		}
		content, err := os.ReadFile(target)
		if err != nil || string(content) != "old: 1\n" {
			t.Fatalf("target changed on rollback: %q %v", content, err)
		}
	})

	t.Run("setup failure leaves temp pending and swept by finalise", func(t *testing.T) {
		run := newTestRun(t)
		handler := NewWriteInPlaceHandler(filepath.Join(t.TempDir(), "missing.yml"))
		_, err := handler.CreateTempFile()
		if err == nil {
			t.Fatal("expected stat error for missing input")
		}
		run.mu.Lock()
		pending := len(run.entries)
		tempPath := run.entries[0].name[len("temp file: "):]
		run.mu.Unlock()
		if pending != 1 {
			t.Fatalf("expected 1 pending entry, got %d", pending)
		}

		if _, err := run.Finalise(false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(tempPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pending temp should be swept after failed setup: %v", err)
		}
	})
}

// Failed copy fallback (simulated by a symlink pointing at a directory) must
// surface the wrapped error and remove the source temp when finalised.
func TestTryRenameFileCopyFallbackFailureRollsBackTemp(t *testing.T) {
	run := newTestRun(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.tmp")
	dstDir := filepath.Join(dir, "targetdir")
	if err := os.Mkdir(dstDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dstDir, link); err != nil {
		t.Skip("symlinks not supported in this environment")
	}

	// Register rename resource whose commit will fail copying into a dir.
	run.registerRename(src, nil, link)
	_, err := run.Finalise(true)
	if err == nil {
		t.Fatal("expected copy fallback failure")
	}
	want := "failed copying from"
	if got := err.Error(); !strings.Contains(got, want) {
		t.Fatalf("error text changed: got %q want contains %q", got, want)
	}
	if _, statErr := os.Stat(src); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("source temp should be removed after failed copy rollback: %v", statErr)
	}
}
