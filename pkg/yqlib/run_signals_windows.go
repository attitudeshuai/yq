//go:build windows

package yqlib

import (
	"os"
	"os/signal"
	"syscall"
)

// installInterruptHandler watches console interrupts. The Go runtime maps
// CTRL_C_EVENT and CTRL_BREAK_EVENT to os.Interrupt, and console close/logoff/
// shutdown to SIGTERM. The first signal cooperatively cancels the run; any
// later signal terminates the process immediately with exit code 130,
// skipping finalisation.
func installInterruptHandler(run *Run) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go watchInterrupts(run, ch)
	run.stopWatching = func() {
		signal.Stop(ch)
		close(ch)
	}
}
