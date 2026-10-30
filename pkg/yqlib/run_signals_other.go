//go:build plan9 || js || wasip1

package yqlib

// installInterruptHandler is a no-op on platforms without the supported
// interrupt/terminate signal semantics: the run registry and cooperative
// cancellation still work, but external signals keep pre-change behaviour.
func installInterruptHandler(_ *Run) {
}
