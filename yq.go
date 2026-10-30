package main

import (
	"errors"
	"os"

	command "github.com/mikefarah/yq/v4/cmd"
	"github.com/mikefarah/yq/v4/pkg/yqlib"
)

func main() {
	os.Exit(run())
}

func run() int {
	return runWithArgs(os.Args[1:])
}

// runWithArgs executes one yq invocation behind the finalisation registry and
// interrupt handler. It is split out from run for testability.
func runWithArgs(args []string) int {
	// BeginRun installs the interrupt handler and the finalisation registry
	// used by the whole output chain.
	run := yqlib.BeginRun()

	cmd := command.New()

	_, _, err := cmd.Find(args)
	if err == nil {
		// Pin the args explicitly: os.Args is not the yq args when this runs
		// programmatically (e.g. the interrupt end-to-end tests).
		cmd.SetArgs(args)
	} else if len(args) > 0 && args[0] != "__complete" && args[0] != "__completeNoDesc" {
		// default command when nothing matches...
		cmd.SetArgs(append([]string{"eval"}, args...))
	}

	cmdErr := cmd.Execute()

	return resolveExitCode(cmdErr, run.Cancelled())
}

// resolveExitCode maps the command outcome onto process exit codes:
// interruption (whether returned as the sentinel or merely flagged by the
// run) exits 130; any other error exits 1; success exits 0. A non-interrupt
// write error wins over a cancellation flag that raced with it.
func resolveExitCode(err error, interrupted bool) int {
	switch {
	case errors.Is(err, yqlib.ErrInterrupted):
		return yqlib.InterruptExitCode
	case err != nil:
		return 1
	case interrupted:
		return yqlib.InterruptExitCode
	default:
		return 0
	}
}
