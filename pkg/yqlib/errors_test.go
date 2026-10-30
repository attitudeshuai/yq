package yqlib

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/mikefarah/yq/v4/test"
)

func TestSourceErrorStageRendering(t *testing.T) {
	base := errors.New("kaput")

	scenarios := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "decode frame",
			err:      newDecodeError(base, "test.yml"),
			expected: "bad file 'test.yml': kaput",
		},
		{
			name:     "load frame",
			err:      newLoadError(base, "cat.yml"),
			expected: "failed to load cat.yml: kaput",
		},
		{
			name:     "split output frame",
			err:      newSplitOutputError(base, "out/thing.yml"),
			expected: "failed to write split output 'out/thing.yml': kaput",
		},
		{
			name:     "write in place frame",
			err:      NewWriteInPlaceError(base, "config.yml"),
			expected: "failed to write in place to 'config.yml': kaput",
		},
		{
			name:     "single file first document renders no prefix",
			err:      newDocumentError(base, "config.yml", 0, false),
			expected: "kaput",
		},
		{
			name:     "multi file first document renders name and index",
			err:      newDocumentError(base, "config.yml", 0, true),
			expected: "config.yml: document index 0: kaput",
		},
		{
			name:     "named later document renders name and index",
			err:      newDocumentError(base, "config.yml", 2, false),
			expected: "config.yml: document index 2: kaput",
		},
		{
			name:     "anonymous later document renders index only",
			err:      newDocumentError(base, "", 3, false),
			expected: "document index 3: kaput",
		},
		{
			name:     "anonymous first document is not framed",
			err:      newDocumentError(base, "", 0, false),
			expected: "kaput",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			test.AssertResult(t, scenario.expected, scenario.err.Error())
		})
	}
}

func TestSourceErrorNestedRendering(t *testing.T) {
	pathErr := &fs.PathError{Op: "open", Path: "cat.yml", Err: os.ErrNotExist}
	err := newLoadError(pathErr, "cat.yml")
	err = newDocumentError(err, "outer.yml", 1, false)

	test.AssertResult(t, "outer.yml: document index 1: failed to load cat.yml: open cat.yml: file does not exist", err.Error())

	locations := SourceLocations(err)
	test.AssertResult(t, 2, len(locations))
	test.AssertResult(t, DocumentStage, locations[0].Stage)
	test.AssertResult(t, "outer.yml", locations[0].Filename)
	test.AssertResult(t, uint(1), locations[0].DocumentIndex)
	test.AssertResult(t, LoadStage, locations[1].Stage)
	test.AssertResult(t, "cat.yml", locations[1].Target)
}

func TestSourceErrorSameStageIdempotent(t *testing.T) {
	base := errors.New("kaput")
	inner := newDocumentError(base, "inner.yml", 1, false)
	outer := newDocumentError(inner, "outer.yml", 2, true)

	// the layer closest to the failure site wins, no duplicated prefix
	test.AssertResult(t, "inner.yml: document index 1: kaput", outer.Error())
	test.AssertResult(t, 1, len(SourceLocations(outer)))
}

func TestSourceErrorUnwrapToUnderlying(t *testing.T) {
	pathErr := &fs.PathError{Op: "open", Path: "missing.yml", Err: os.ErrNotExist}
	err := newDecodeError(pathErr, "missing.yml")

	var target *fs.PathError
	test.AssertResult(t, true, errors.As(err, &target))
	test.AssertResult(t, "missing.yml", target.Path)
}

func TestSourceErrorNilPassThrough(t *testing.T) {
	test.AssertResult(t, error(nil), newDecodeError(nil, "x.yml"))
	test.AssertResult(t, error(nil), newDocumentError(nil, "x.yml", 1, false))
	test.AssertResult(t, error(nil), newLoadError(nil, "x.yml"))
	test.AssertResult(t, error(nil), newSplitOutputError(nil, "x.yml"))
	test.AssertResult(t, error(nil), NewWriteInPlaceError(nil, "x.yml"))
	test.AssertResult(t, 0, len(SourceLocations(errors.New("plain"))))
}

func TestNewDocumentErrorForCandidate(t *testing.T) {
	base := errors.New("kaput")
	firstDoc := &CandidateNode{filename: "sample.yml", document: 0}
	laterDoc := &CandidateNode{filename: "sample.yml", document: 4}

	test.AssertResult(t, "kaput", newDocumentErrorForCandidate(base, firstDoc, false).Error())
	test.AssertResult(t, "sample.yml: document index 4: kaput", newDocumentErrorForCandidate(base, laterDoc, false).Error())
	test.AssertResult(t, base, newDocumentErrorForCandidate(base, nil, false))
}
