//go:build !windows && !plan9 && !js && !wasip1

package yqlib

import (
	"os"
	"os/signal"
	"syscall"
)

// installInterruptHandler watches for interrupt and terminate signals. The
// first signal cooperatively cancels the run; any later signal terminates the
// process immediately with exit code 130, skipping finalisation.
func installInterruptHandler(run *Run) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go watchInterrupts(run, ch)
	run.stopWatching = func() {
		signal.Stop(ch)
		close(ch)
	}
}
