package main

import (
	"errors"
	"testing"

	"github.com/mikefarah/yq/v4/pkg/yqlib"
)

func TestResolveExitCode(t *testing.T) {
	otherErr := errors.New("boom")
	cases := []struct {
		name        string
		err         error
		interrupted bool
		want        int
	}{
		{"success", nil, false, 0},
		{"ordinary error", otherErr, false, 1},
		{"sentinel interruption", yqlib.ErrInterrupted, false, 130},
		{"flagged interruption after success", nil, true, 130},
		{"wrapped interruption", wrapErr(yqlib.ErrInterrupted), false, 130},
		{"write error wins over cancellation flag", otherErr, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveExitCode(tc.err, tc.interrupted); got != tc.want {
				t.Fatalf("resolveExitCode(%v, %v) = %d, want %d", tc.err, tc.interrupted, got, tc.want)
			}
		})
	}
}

func wrapErr(err error) error {
	return errors.Join(err)
}
