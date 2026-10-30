package yqlib

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestRun(t *testing.T) *Run {
	t.Helper()
	run := BeginRun()
	t.Cleanup(run.EndRun)
	return run
}

func TestOutputRunCancellation(t *testing.T) {
	run := newTestRun(t)

	if run.Cancelled() {
		t.Fatal("new run should not be cancelled")
	}
	if err := currentRunCancelled(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	run.Cancel()

	if !run.Cancelled() {
		t.Fatal("expected cancelled")
	}
	if !errors.Is(currentRunCancelled(), ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", currentRunCancelled())
	}

	// Cancel is idempotent.
	run.Cancel()
}

func TestOutputRunNoopIsInert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	entry := noopRunInstance.registerScratchFile(path)
	// Registration did not remove anything...
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("noop registration must be inert: %v", err)
	}
	// ...but an explicit transition runs immediately.
	entry.Rollback()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected file removed, got %v", err)
	}

	// Sink registration and completion are no-ops under the noop run.
	buf := bytes.NewBuffer(nil)
	w := bufio.NewWriter(buf)
	noopRunInstance.registerSink("stdout", w, nil, false)
	noopRunInstance.markWriterComplete(w)
	if _, err := w.WriteString("kept in buffer"); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatal("noop run must not flush sinks")
	}
}

func TestOutputRunTempFileCommitAndRollback(t *testing.T) {
	run := newTestRun(t)
	dir := t.TempDir()

	target := filepath.Join(dir, "target.yml")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "tmp")
	if err := os.WriteFile(tmp, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	entry := run.registerRename(tmp, nil, target)

	// Pending: nothing moved yet.
	if content, err := os.ReadFile(target); err != nil || string(content) != "old" {
		t.Fatalf("target changed before commit: %q %v", content, err)
	}

	interrupted, err := run.Finalise(true)
	if err != nil || interrupted {
		t.Fatalf("finalise commit: err=%v interrupted=%v", err, interrupted)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "new" {
		t.Fatalf("target not renamed: %q %v", content, err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp still exists after commit")
	}

	// Finalise is one-shot; a second call does nothing.
	interrupted, err = run.Finalise(false)
	if err != nil || interrupted {
		t.Fatalf("second finalise should be inert, got err=%v interrupted=%v", err, interrupted)
	}
	_ = entry
}

func TestOutputRunTempFileRollback(t *testing.T) {
	run := newTestRun(t)
	dir := t.TempDir()

	target := filepath.Join(dir, "target.yml")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "tmp")
	if err := os.WriteFile(tmp, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	run.registerRename(tmp, nil, target)

	interrupted, err := run.Finalise(false)
	if err != nil || interrupted {
		t.Fatalf("finalise rollback: err=%v interrupted=%v", err, interrupted)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "old" {
		t.Fatalf("target changed on rollback: %q %v", content, err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temp not removed on rollback")
	}
}

func TestOutputRunExplicitTransitionsAreIdempotent(t *testing.T) {
	run := newTestRun(t)
	dir := t.TempDir()

	committed := filepath.Join(dir, "committed")
	if err := os.WriteFile(committed, []byte("c"), 0600); err != nil {
		t.Fatal(err)
	}
	cEntry := run.registerScratchFile(committed)
	cEntry.Commit()
	cEntry.Commit() // must not error or re-run
	cEntry.Rollback()
	// Deferred: still present until Finalise.
	if _, err := os.Stat(committed); err != nil {
		t.Fatalf("deferred entry must not act before finalise: %v", err)
	}

	rolledBack := filepath.Join(dir, "rolledback")
	if err := os.WriteFile(rolledBack, []byte("r"), 0600); err != nil {
		t.Fatal(err)
	}
	rEntry := run.registerScratchFile(rolledBack)
	rEntry.Rollback()
	rEntry.Rollback()
	rEntry.Commit() // no-op: already rolled back

	if _, err := run.Finalise(true); err != nil {
		t.Fatalf("finalise with pre-resolved entries: %v", err)
	}
	if _, err := os.Stat(committed); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("committed scratch file should have been removed by finalise")
	}
	if _, err := os.Stat(rolledBack); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rolled back file should be removed by finalise")
	}
}

func TestOutputRunReverseResolutionOrder(t *testing.T) {
	run := newTestRun(t)
	var order []string
	mkResource := func(name string) registeredResource {
		return &callbackResource{
			commitFn:   func() error { order = append(order, "commit:"+name); return nil },
			rollbackFn: func() { order = append(order, "rollback:"+name) },
		}
	}
	run.register(mkResource("a"), "a")
	run.register(mkResource("b"), "b")
	run.register(mkResource("c"), "c")

	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}
	got := order[0] + "," + order[1] + "," + order[2]
	if got != "rollback:c,rollback:b,rollback:a" {
		t.Fatalf("expected reverse rollback order, got %v", order)
	}
}

func TestOutputRunCommitErrorRollsBackEntry(t *testing.T) {
	run := newTestRun(t)
	wantErr := errors.New("disk on fire")
	var rolledBack bool
	run.register(&callbackResource{
		commitFn:   func() error { return wantErr },
		rollbackFn: func() { rolledBack = true },
	}, "failing")

	_, err := run.Finalise(true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected commit error to surface, got %v", err)
	}
	if !rolledBack {
		t.Fatal("failed commit should trigger rollback")
	}
}

type trackedCloser struct {
	closed bool
}

func (t *trackedCloser) Close() error {
	t.closed = true
	return nil
}

func TestOutputRunCloserResource(t *testing.T) {
	t.Run("active run closes on finalise", func(t *testing.T) {
		run := newTestRun(t)
		closer := &trackedCloser{}
		run.registerCloser(closer, "input file")
		if closer.closed {
			t.Fatal("registered closer must not close before finalise")
		}
		if _, err := run.Finalise(true); err != nil {
			t.Fatal(err)
		}
		if !closer.closed {
			t.Fatal("closer should be closed after finalise")
		}
	})

	t.Run("noop run stays inert", func(t *testing.T) {
		closer := &trackedCloser{}
		noopRunInstance.registerCloser(closer, "input file")
		if closer.closed {
			t.Fatal("noop closer registration must be inert")
		}
	})
}

func TestOutputRunSinkCompleteKeptAndClosed(t *testing.T) {
	run := newTestRun(t)
	path := filepath.Join(t.TempDir(), "out.yml")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	if _, err := w.WriteString("a: 1\n"); err != nil {
		t.Fatal(err)
	}
	run.registerSink(path, w, f, true)
	run.markWriterComplete(w)

	if _, err := run.Finalise(true); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("complete sink file missing: %v", err)
	}
	if string(content) != "a: 1\n" {
		t.Fatalf("complete sink content lost: %q", content)
	}
	// Handle is closed: reopening for writing must succeed (Windows denies
	// open handles).
	if rf, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600); err != nil {
		t.Fatalf("sink file handle not closed: %v", err)
	} else {
		rf.Close()
	}
}

func TestOutputRunSinkPartialDeleted(t *testing.T) {
	run := newTestRun(t)
	path := filepath.Join(t.TempDir(), "out.yml")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	if _, err := w.WriteString("partial"); err != nil {
		t.Fatal(err)
	}
	run.registerSink(path, w, f, true)
	// Not marked complete: partial. Finalise failure (and success alike).
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial sink should be deleted regardless of run outcome")
	}
}

func TestOutputRunSinkNonFileFlushedAndNotClosed(t *testing.T) {
	run := newTestRun(t)
	buf := &bytes.Buffer{}
	closer := &trackedCloser{}
	w := bufio.NewWriter(struct {
		io.Writer
		io.Closer
	}{buf, closer})
	if _, err := w.WriteString("tail"); err != nil {
		t.Fatal(err)
	}
	// Non-file sinks never carry a closer in production, but the resolve
	// contract is that the underlying writer is not closed.
	run.registerSink("<stdout>", w, nil, false)
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "tail" {
		t.Fatalf("stdout buffer not flushed: %q", buf.String())
	}
	if closer.closed {
		t.Fatal("non-file sink must not be closed")
	}
}

func TestOutputRunCopyFallbackRollback(t *testing.T) {
	run := newTestRun(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.tmp")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("orig"), 0600); err != nil {
		t.Fatal(err)
	}
	entry := run.registerCopyFallback(src, dst)
	entry.Rollback()
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed copy fallback should remove the source temp on rollback")
	}
}

func TestOutputRunBeginEndNesting(t *testing.T) {
	outer := BeginRun()
	if ActiveRun() != outer {
		t.Fatal("expected outer run active")
	}
	inner := BeginRun()
	if ActiveRun() != inner {
		t.Fatal("expected inner run active")
	}
	inner.Cancel()
	inner.EndRun()
	if ActiveRun() != outer {
		t.Fatal("expected outer run restored")
	}
	if outer.Cancelled() {
		t.Fatal("inner cancellation leaked to outer run")
	}
	outer.EndRun()
	if ActiveRun() != noopRunInstance {
		t.Fatal("expected noop run after ending all")
	}
}

func TestOutputRunConcurrentCancelAndRegister(t *testing.T) {
	run := newTestRun(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			run.Cancel()
		}()
		go func() {
			defer wg.Done()
			buf := bytes.NewBuffer(nil)
			run.registerSink("<stdout>", bufio.NewWriter(buf), nil, false)
		}()
	}
	wg.Wait()
	if !run.Cancelled() {
		t.Fatal("expected cancelled")
	}
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}
}
