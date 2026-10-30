//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

const createNewProcessGroup = 0x00000200

func configureChildSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// sendInterrupt delivers CTRL_BREAK_EVENT to the child's process group. The Go
// runtime maps CTRL_BREAK to os.Interrupt, exactly like CTRL_C.
func sendInterrupt(t *testing.T, p *os.Process) {
	t.Helper()
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GenerateConsoleCtrlEvent")
	r1, _, err := proc.Call(uintptr(1) /* CTRL_BREAK_EVENT */, uintptr(p.Pid))
	if r1 == 0 {
		t.Skipf("GenerateConsoleCtrlEvent unavailable in this environment: %v", err)
	}
}
