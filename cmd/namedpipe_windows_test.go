//go:build windows

package cmd

import (
	"os"
	"testing"
)

func openNamedPipeForTest(t *testing.T, _ string) *os.File {
	t.Helper()
	t.Skip("named pipes not supported by this test on windows")
	return nil
}
