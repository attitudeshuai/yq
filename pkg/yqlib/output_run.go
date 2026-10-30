package yqlib

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
)

// ErrInterrupted is returned at evaluation and printing boundaries once a run
// has been cancelled (e.g. by an interrupt signal). It is a sentinel: use
// errors.Is rather than matching its text. The command layer never prints it.
var ErrInterrupted = errors.New("interrupted")

// InterruptExitCode is the conventional exit code (128 + SIGINT) used when a
// run finishes because it was interrupted.
const InterruptExitCode = 130

// watchInterrupts implements the signal policy: the first interrupt only
// requests cancellation so the run can stop at a safe boundary and finalise;
// any further signal terminates immediately without cleanup. The signal
// channel is buffered so two rapid signals are not coalesced.
func watchInterrupts(run *Run, ch <-chan os.Signal) {
	if _, ok := <-ch; !ok {
		return
	}
	run.Cancel()
	for range ch {
		os.Exit(InterruptExitCode)
	}
}

type resourceState uint8

const (
	statePending resourceState = iota
	stateCommitted
	stateRolledBack
)

// registeredResource is a lifecycle managed entry on the run registry.
// commit failures are surfaced to the caller; rollback is best effort and
// only logs, matching the previous tryRemoveTempFile behaviour.
type registeredResource interface {
	commit() error
	rollback()
}

// registryEntry is the state machine wrapper shared by every registered
// resource. Transitions are idempotent: committing or rolling back an entry
// that already reached a terminal state is a no-op.
//
// On an active run entries are deferred: explicit Commit/Rollback only record
// the outcome and the action runs once during Finalise, in reverse registration
// order. On the ambient no-op run entries are eager, so handlers keep their
// pre-registry behaviour without a BeginRun.
type registryEntry struct {
	name     string
	mu       sync.Mutex
	state    resourceState
	done     bool
	deferred bool
	res      registeredResource
}

// Commit records/commits the entry as committed. Deferred entries do not act
// until Finalise; eager entries run the commit callback immediately.
func (e *registryEntry) Commit() error {
	e.mu.Lock()
	if e.state != statePending {
		e.mu.Unlock()
		return nil
	}
	deferred := e.deferred
	e.state = stateCommitted
	e.mu.Unlock()

	if deferred {
		return nil
	}
	if err := e.res.commit(); err != nil {
		e.mu.Lock()
		e.state = statePending
		e.mu.Unlock()
		return err
	}
	e.mu.Lock()
	e.done = true
	e.mu.Unlock()
	return nil
}

// Rollback records/performs rollback. Deferred entries act during Finalise.
func (e *registryEntry) Rollback() {
	e.mu.Lock()
	if e.state != statePending {
		e.mu.Unlock()
		return
	}
	deferred := e.deferred
	e.state = stateRolledBack
	e.mu.Unlock()

	if !deferred {
		e.res.rollback()
		e.mu.Lock()
		e.done = true
		e.mu.Unlock()
	}
}

// resolveCommit runs the commit action once. Used by Finalise for entries that
// are (or became) committed.
func (e *registryEntry) resolveCommit() error {
	e.mu.Lock()
	if e.done {
		e.mu.Unlock()
		return nil
	}
	e.state = stateCommitted
	e.mu.Unlock()

	err := e.res.commit()

	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.state = statePending
		return err
	}
	e.done = true
	return nil
}

// resolveRollback runs the rollback action once. Used by Finalise.
func (e *registryEntry) resolveRollback() {
	e.mu.Lock()
	if e.done {
		e.mu.Unlock()
		return
	}
	e.state = stateRolledBack
	e.mu.Unlock()

	e.res.rollback()

	e.mu.Lock()
	e.done = true
	e.mu.Unlock()
}

// Run is the process-wide finalisation registry for one yq invocation. Every
// temporary artefact, output sink and reader created during the output chain
// registers itself here; Finalise resolves all still-pending entries in
// reverse registration order.
type Run struct {
	noop bool

	cancelled atomic.Bool
	finalised atomic.Bool

	finaliseMu sync.Mutex
	endOnce    sync.Once

	mu      sync.Mutex
	entries []*registryEntry
	sinks   map[*bufio.Writer]*sinkResource

	stopWatching func()
	previous     *Run
}

// BeginRun creates a new registry, installs the interrupt handler and makes it
// the active run. EndRun restores the previous active run.
func BeginRun() *Run {
	run := &Run{sinks: map[*bufio.Writer]*sinkResource{}}
	installInterruptHandler(run)

	activeRunMu.Lock()
	run.previous = activeRun
	activeRun = run
	activeRunMu.Unlock()

	return run
}

// EndRun detaches the run, restoring the run that was active before it.
// Safe to call more than once.
func (r *Run) EndRun() {
	if r == nil || r.noop {
		return
	}
	r.endOnce.Do(func() {
		activeRunMu.Lock()
		if activeRun == r {
			activeRun = r.previous
		}
		stop := r.stopWatching
		activeRunMu.Unlock()

		if stop != nil {
			stop()
		}
	})
}

var (
	activeRunMu sync.RWMutex
	activeRun   = noopRunInstance
)

// noopRunInstance is the ambient run used by library callers and tests that
// never called BeginRun: registrations are inert (resources follow their
// pre-registry lifecycle), and cancellation can never trigger.
var noopRunInstance = &Run{noop: true}

// ActiveRun returns the current run, or the process-wide no-op run.
func ActiveRun() *Run {
	activeRunMu.RLock()
	defer activeRunMu.RUnlock()
	return activeRun
}

// Cancel requests cooperative cancellation of the running evaluation.
func (r *Run) Cancel() {
	if r == nil || r.noop {
		return
	}
	r.cancelled.Store(true)
}

// Cancelled reports whether Cancel was called.
func (r *Run) Cancelled() bool {
	return r != nil && !r.noop && r.cancelled.Load()
}

func (r *Run) checkCancelled() error {
	if r.Cancelled() {
		return ErrInterrupted
	}
	return nil
}

// currentRunCancelled returns ErrInterrupted when the active run has been
// cancelled. It is the hot-loop check used by evaluators and printers.
func currentRunCancelled() error {
	return ActiveRun().checkCancelled()
}

func (r *Run) register(res registeredResource, name string) *registryEntry {
	if r == nil || r.noop {
		// Eager entry: explicit Commit/Rollback calls run immediately so
		// handlers keep their pre-registry behaviour without an active run.
		return &registryEntry{name: name, state: statePending, res: res}
	}
	entry := &registryEntry{name: name, state: statePending, deferred: true, res: res}
	r.mu.Lock()
	r.entries = append(r.entries, entry)
	r.mu.Unlock()
	return entry
}

// registerRename tracks a temporary file that is renamed (or copy-fallback
// written) over target on commit and deleted on rollback.
func (r *Run) registerRename(path string, closer io.Closer, target string) *registryEntry {
	file := &onceCloser{closer: closer, name: path}
	return r.register(&callbackResource{
		commitFn: func() error {
			file.close()
			return tryRenameFile(path, target)
		},
		rollbackFn: func() {
			file.close()
			removeRegisteredFile(path)
		},
	}, "temp file: "+path)
}

// registerScratchFile tracks an intermediate file that is always removed once
// the run finishes, whatever the outcome.
func (r *Run) registerScratchFile(path string) *registryEntry {
	remove := func() { removeRegisteredFile(path) }
	return r.register(&callbackResource{
		commitFn:   func() error { remove(); return nil },
		rollbackFn: remove,
	}, "scratch file: "+path)
}

// registerCopyFallback tracks a rename fallback that copies contents from src
// to dst. A failed copy leaves the source temp file pending so the registry
// removes it during finalisation.
func (r *Run) registerCopyFallback(src string, dst string) *registryEntry {
	return r.register(&callbackResource{
		commitFn:   func() error { return nil },
		rollbackFn: func() { removeRegisteredFile(src) },
	}, "copy fallback: "+src+" -> "+dst)
}

// registerCloser tracks a reader/handle that must be closed whatever the
// outcome. Under the no-op run registration is inert.
func (r *Run) registerCloser(closer io.Closer, name string) *registryEntry {
	file := &onceCloser{closer: closer, name: name}
	close := func() error { file.close(); return nil }
	return r.register(&callbackResource{
		commitFn:   close,
		rollbackFn: file.close,
	}, "closer: "+name)
}

// registerSink tracks a buffered output writer (stdout or a split file).
func (r *Run) registerSink(name string, writer *bufio.Writer, closer io.Closer, isFile bool) *registryEntry {
	sink := &sinkResource{
		name:   name,
		writer: writer,
		closer: closer,
		isFile: isFile,
	}
	entry := r.register(sink, "output sink: "+name)
	if r != nil && !r.noop {
		r.mu.Lock()
		r.sinks[writer] = sink
		r.mu.Unlock()
	}
	return entry
}

// markWriterComplete flags the sink backing the given buffered writer as
// fully written. Split files marked complete are kept; unmarked ones are
// deleted during finalisation.
func (r *Run) markWriterComplete(writer *bufio.Writer) {
	if r == nil || r.noop {
		return
	}
	r.mu.Lock()
	sink := r.sinks[writer]
	r.mu.Unlock()
	if sink != nil {
		sink.complete.Store(true)
	}
}

// Finalise resolves every registered resource in reverse registration order:
// pending entries are committed on success and rolled back on failure; entries
// already committed or rolled back are skipped. A failed commit is returned
// and the entry is rolled back. It returns whether the run was cancelled and
// the first commit error encountered.
func (r *Run) Finalise(success bool) (bool, error) {
	if r == nil || r.noop {
		return false, nil
	}
	r.finaliseMu.Lock()
	defer r.finaliseMu.Unlock()
	if r.finalised.Load() {
		return r.cancelled.Load(), nil
	}

	r.mu.Lock()
	entries := make([]*registryEntry, len(r.entries))
	copy(entries, r.entries)
	r.mu.Unlock()

	var commitErr error
	seen := len(entries)
	var resolve func(entry *registryEntry)
	resolve = func(entry *registryEntry) {
		entry.mu.Lock()
		state := entry.state
		done := entry.done
		entry.mu.Unlock()
		if !done {
			commit := success && state != stateRolledBack
			if !commit {
				entry.resolveRollback()
			} else if err := entry.resolveCommit(); err != nil {
				if commitErr == nil {
					commitErr = err
				}
				entry.resolveRollback()
			}
		}

		// Entries registered by this callback (e.g. the rename copy
		// fallback) resolve before older entries, in reverse order.
		r.mu.Lock()
		var tail []*registryEntry
		if len(r.entries) > seen {
			tail = append(tail, r.entries[seen:]...)
			seen = len(r.entries)
		}
		r.mu.Unlock()
		for i := len(tail) - 1; i >= 0; i-- {
			resolve(tail[i])
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		resolve(entries[i])
	}

	r.finalised.Store(true)
	return r.cancelled.Load(), commitErr
}

// callbackResource adapts plain functions to registeredResource.
type callbackResource struct {
	commitFn   func() error
	rollbackFn func()
}

func (c *callbackResource) commit() error {
	return c.commitFn()
}

func (c *callbackResource) rollback() {
	c.rollbackFn()
}

// sinkResource is a buffered output writer plus its optional file handle.
type sinkResource struct {
	name     string
	writer   *bufio.Writer
	closer   io.Closer
	isFile   bool
	complete atomic.Bool
}

func (s *sinkResource) resolve() {
	if s.isFile {
		if s.complete.Load() {
			if err := s.writer.Flush(); err != nil {
				log.Debugf("Error flushing output file %v: %v", s.name, err)
			}
			if s.closer != nil {
				if err := s.closer.Close(); err != nil {
					log.Errorf("Error closing file %v: %v", s.name, err)
				}
			}
			return
		}
		// Partial output: discard the unflushed tail, close and remove.
		if s.closer != nil {
			if err := s.closer.Close(); err != nil {
				log.Errorf("Error closing file %v: %v", s.name, err)
			}
		}
		removeRegisteredFile(s.name)
		return
	}
	// stdout (or another externally owned writer): flush what we produced,
	// never close the underlying writer.
	if err := s.writer.Flush(); err != nil {
		log.Debugf("Error flushing output: %v", err)
	}
}

func (s *sinkResource) commit() error {
	s.resolve()
	return nil
}

func (s *sinkResource) rollback() {
	s.resolve()
}

// removeRegisteredFile deletes a path, treating a missing file as already
// cleaned up (so state-machine retries stay silent).
func removeRegisteredFile(path string) {
	log.Debugf("Removing temp file: %v", path)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Errorf("Failed to remove temp file: %v", path)
	}
}

// onceCloser closes its handle at most once, so a commit that closes the file
// and then fails can be rolled back without logging a second close.
type onceCloser struct {
	closer io.Closer
	name   string
	once   sync.Once
}

func (c *onceCloser) close() {
	if c == nil || c.closer == nil {
		return
	}
	c.once.Do(func() {
		if err := c.closer.Close(); err != nil {
			log.Errorf("Error closing file %v: %v", c.name, err)
		}
	})
}
