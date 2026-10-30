package yqlib

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cancelAfterNWriter cancels after the nth split writer has been opened.
type cancelAfterNWriter struct {
	inner PrinterWriter
	run   *Run
	n     int
	count int
}

func (c *cancelAfterNWriter) GetWriter(node *CandidateNode) (*bufio.Writer, error) {
	w, err := c.inner.GetWriter(node)
	if err != nil {
		return nil, err
	}
	c.count++
	if c.count >= c.n {
		c.run.Cancel()
	}
	return w, nil
}

// End-to-end (library level) stream chain: split writer opens per document,
// cancellation after the 5th file must keep exactly the complete files and
// never open the rest.
func TestInterruptStreamSplitIntegration(t *testing.T) {
	InitExpressionParser()
	run := newTestRun(t)
	dir := t.TempDir()

	const total = 20
	const keep = 5
	var input strings.Builder
	for i := 0; i < total; i++ {
		fmt.Fprintf(&input, "name: doc_%03d\n", i)
		if i < total-1 {
			input.WriteString("---\n")
		}
	}
	nameExpr, err := ExpressionParser.ParseExpression(`"` + filepath.ToSlash(dir) + `/" + .name`)
	if err != nil {
		t.Fatal(err)
	}
	multi := NewMultiPrinterWriter(nameExpr, YamlFormat)
	cw := &cancelAfterNWriter{inner: multi, run: run, n: keep}
	printer := NewPrinter(NewYamlEncoder(ConfiguredYamlPreferences), cw)
	nodeExpr, err := ExpressionParser.ParseExpression(".")
	if err != nil {
		t.Fatal(err)
	}

	docs, err := NewStreamEvaluator().Evaluate(
		"big.yml",
		strings.NewReader(input.String()),
		nodeExpr,
		printer,
		NewYamlDecoder(ConfiguredYamlPreferences),
	)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v (docs=%d)", err, docs)
	}
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < total; i++ {
		path := filepath.Join(dir, fmt.Sprintf("doc_%03d.yml", i))
		got, readErr := os.ReadFile(path)
		if i < keep {
			if readErr != nil {
				t.Fatalf("complete file %d missing: %v", i, readErr)
			}
			sep := ""
			if i > 0 {
				// split files for documents after the first keep the doc separator
				sep = "---\n"
			}
			want := fmt.Sprintf("%sname: doc_%03d\n", sep, i)
			if string(got) != want {
				t.Fatalf("complete file %d content mismatch: got %q want %q", i, string(got), want)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			t.Fatalf("file %d must not exist after cancellation, got: %q", i, string(got))
		}
	}
}

// In-place chain interrupted mid-stream: the original file stays untouched and
// the temp file is swept by the registry.
func TestInterruptInPlaceRollbackIntegration(t *testing.T) {
	InitExpressionParser()
	run := newTestRun(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "f.yml")
	original := "a: 1\nb: 2\n"
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	handler := NewWriteInPlaceHandler(target)
	temp, err := handler.CreateTempFile()
	if err != nil {
		t.Fatal(err)
	}
	tempPath := temp.Name()

	spw := NewSinglePrinterWriter(temp)
	printer := NewPrinter(NewYamlEncoder(ConfiguredYamlPreferences), spw)
	nodeExpr, _ := ExpressionParser.ParseExpression(".")
	input := "a: 1\n---\na: 2\n---\na: 3\n"

	cancelPrinter := &cancelAfterPrinter{inner: printer, run: run}
	// cancelAfterPrinter cancels after the first result
	_, err = NewStreamEvaluator().Evaluate(
		"f.yml",
		strings.NewReader(input),
		nodeExpr,
		cancelPrinter,
		NewYamlDecoder(ConfiguredYamlPreferences),
	)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("target changed after interrupted in-place write: %q", string(got))
	}
	if _, err := os.Stat(tempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("in-place temp file should be rolled back: %v", err)
	}
}
