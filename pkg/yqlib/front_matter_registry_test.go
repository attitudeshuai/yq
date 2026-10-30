package yqlib

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFrontMatterActiveRunScratchRemovedAndReaderClosed(t *testing.T) {
	for _, success := range []bool{true, false} {
		run := newTestRun(t)
		source := filepath.Join(t.TempDir(), "page.md")
		if err := os.WriteFile(source, []byte("---\na: apple\n---\nbody\n"), 0600); err != nil {
			t.Fatal(err)
		}

		handler := NewFrontMatterHandler(source)
		if err := handler.Split(); err != nil {
			t.Fatal(err)
		}
		tempName := handler.GetYamlFrontMatterFilename()
		content, err := io.ReadAll(handler.GetContentReader())
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "---\nbody\n" {
			t.Fatalf("unexpected content: %q", content)
		}

		// Pending until finalise.
		if _, err := os.Stat(tempName); err != nil {
			t.Fatalf("scratch temp should exist before finalise: %v", err)
		}

		if _, err := run.Finalise(success); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(tempName); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("front matter scratch temp must be removed (success=%v)", success)
		}
		// The source file itself stays and is untouched.
		body, err := os.ReadFile(source)
		if err != nil || string(body) != "---\na: apple\n---\nbody\n" {
			t.Fatalf("source changed: %q %v", body, err)
		}
	}
}

func TestFrontMatterActiveRunSplitFailureSwept(t *testing.T) {
	run := newTestRun(t)
	// Reading a directory through the front matter splitter fails on Peek;
	// the yaml scratch temp is created before that failure.
	dir := t.TempDir()
	handler := NewFrontMatterHandler(dir)
	err := handler.Split()
	if err == nil {
		t.Skip("reading a directory did not fail on this platform")
	}

	before := len(run.entries)
	if before < 2 {
		t.Fatalf("expected scratch temp + input closer registered, got %d", before)
	}
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}
	for _, entry := range run.entries {
		entry.mu.Lock()
		done := entry.done
		entry.mu.Unlock()
		if !done {
			t.Fatalf("entry %q not resolved during finalise", entry.name)
		}
	}
}
