package yqlib

import (
	"errors"
	"fmt"
)

// ErrorStage identifies the processing stage that contributed a provenance
// frame to the error chain.
type ErrorStage int

const (
	// DecodeStage is opening or decoding an input file.
	DecodeStage ErrorStage = iota
	// DocumentStage is evaluating or printing a particular input document.
	DocumentStage
	// LoadStage is reading a nested file through load/load_str.
	LoadStage
	// SplitOutputStage is naming or writing a split-output file.
	SplitOutputStage
	// WriteInPlaceStage is writing an input file back in place.
	WriteInPlaceStage
)

// SourceLocation is the provenance a single layer contributes to an error
// chain. Not every field applies to every stage:
//   - DecodeStage, DocumentStage and WriteInPlaceStage use Filename (and the
//     document index fields for DocumentStage);
//   - LoadStage and SplitOutputStage use Target.
type SourceLocation struct {
	Stage         ErrorStage
	Filename      string
	HasDocument   bool
	DocumentIndex uint
	Target        string

	// multiFileBatch is the multi-file batch policy snapshot used when
	// rendering document frames. It is captured when the frame is created so
	// rendering never depends on mutable global state.
	multiFileBatch bool
}

// SourceError is one frame in the single processing error chain. Every error
// exit reported by yq goes through a frame of this type; Unwrap keeps the
// underlying error reachable through errors.As/errors.Is.
type SourceError struct {
	location SourceLocation
	cause    error
}

// Location returns the provenance contributed by this frame.
func (e *SourceError) Location() SourceLocation {
	return e.location
}

func (e *SourceError) Error() string {
	cause := e.cause.Error()
	switch e.location.Stage {
	case DecodeStage:
		return fmt.Sprintf("bad file '%v': %v", e.location.Filename, cause)
	case LoadStage:
		return fmt.Sprintf("failed to load %v: %v", e.location.Target, cause)
	case SplitOutputStage:
		return fmt.Sprintf("failed to write split output '%v': %v", e.location.Target, cause)
	case WriteInPlaceStage:
		return fmt.Sprintf("failed to write in place to '%v': %v", e.location.Filename, cause)
	case DocumentStage:
		return documentPrefix(e.location) + cause
	}
	return cause
}

func (e *SourceError) Unwrap() error {
	return e.cause
}

// SourceLocations returns the provenance frames on the error chain, outermost
// frame first. Errors without a SourceError frame return nil.
func SourceLocations(err error) []SourceLocation {
	var locations []SourceLocation
	var frame *SourceError
	for err != nil {
		if !errors.As(err, &frame) {
			break
		}
		locations = append(locations, frame.Location())
		err = frame.Unwrap()
		frame = nil
	}
	return locations
}

// documentPrefix renders the document frame prefix. A single file failing on
// its first (index 0) document renders no prefix, keeping the single file /
// single document output byte-identical to the unframed error; later document
// indexes and multi-file batches carry the source name (when known) and the
// document index.
func documentPrefix(location SourceLocation) string {
	if !location.HasDocument {
		return ""
	}
	if location.DocumentIndex > 0 {
		if location.Filename != "" {
			return fmt.Sprintf("%v: document index %v: ", location.Filename, location.DocumentIndex)
		}
		return fmt.Sprintf("document index %v: ", location.DocumentIndex)
	}
	if location.multiFileBatch && location.Filename != "" {
		return fmt.Sprintf("%v: document index 0: ", location.Filename)
	}
	return ""
}

// withSourceFrame adds a provenance frame. It is idempotent per stage: if the
// chain already carries a frame for the stage, the original error (keeping the
// layer closest to the failure site) is returned unchanged.
func withSourceFrame(err error, location SourceLocation) error {
	if err == nil {
		return nil
	}
	if chainHasStage(err, location.Stage) {
		return err
	}
	return &SourceError{location: location, cause: err}
}

func chainHasStage(err error, stage ErrorStage) bool {
	var frame *SourceError
	for err != nil {
		if !errors.As(err, &frame) {
			return false
		}
		if frame.Location().Stage == stage {
			return true
		}
		err = frame.Unwrap()
		frame = nil
	}
	return false
}

func newDecodeError(err error, filename string) error {
	return withSourceFrame(err, SourceLocation{Stage: DecodeStage, Filename: filename})
}

// newDocumentError adds a document provenance frame. Errors with no filename
// on the first document (null input, string evaluator input) are returned
// untouched so their output stays byte-identical.
func newDocumentError(err error, filename string, documentIndex uint, multiFileBatch bool) error {
	if err == nil {
		return nil
	}
	if filename == "" && documentIndex == 0 {
		return err
	}
	return withSourceFrame(err, SourceLocation{
		Stage:          DocumentStage,
		Filename:       filename,
		HasDocument:    true,
		DocumentIndex:  documentIndex,
		multiFileBatch: multiFileBatch,
	})
}

func newDocumentErrorForCandidate(err error, candidate *CandidateNode, multiFileBatch bool) error {
	if candidate == nil {
		return err
	}
	return newDocumentError(err, candidate.filename, candidate.document, multiFileBatch)
}

func newLoadError(err error, target string) error {
	return withSourceFrame(err, SourceLocation{Stage: LoadStage, Target: target})
}

func newSplitOutputError(err error, target string) error {
	return withSourceFrame(err, SourceLocation{Stage: SplitOutputStage, Target: target})
}

// NewWriteInPlaceError wraps a failure to finish updating an input file in
// place with the input filename. It is exported for the command layer.
func NewWriteInPlaceError(err error, filename string) error {
	if err == nil {
		return nil
	}
	return withSourceFrame(err, SourceLocation{Stage: WriteInPlaceStage, Filename: filename})
}
