package yqlib

import (
	"container/list"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingPrinter struct {
	calls int
	nodes int
}

func (p *recordingPrinter) PrintResults(matchingNodes *list.List) error {
	p.calls++
	if matchingNodes != nil {
		p.nodes += matchingNodes.Len()
	}
	return nil
}

func (p *recordingPrinter) PrintedAnything() bool   { return p.nodes > 0 }
func (p *recordingPrinter) SetAppendix(_ io.Reader) {}
func (p *recordingPrinter) SetNulSepOutput(_ bool)  {}

func TestStreamEvaluatorCancelledAtFileBoundary(t *testing.T) {
	InitExpressionParser()
	newTestRun(t).Cancel()
	printer := &recordingPrinter{}
	err := NewStreamEvaluator().EvaluateFiles(".", []string{"a.yml", "b.yml"}, printer, NewYamlDecoder(ConfiguredYamlPreferences))
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	if printer.calls != 0 {
		t.Fatalf("printer must not be invoked, got %d calls", printer.calls)
	}
}

func TestStreamEvaluatorCancelledAtDocumentBoundary(t *testing.T) {
	newTestRun(t).Cancel()
	printer := &recordingPrinter{}
	node, err := ExpressionParser.ParseExpression(".")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := NewStreamEvaluator().Evaluate(
		"file.yml",
		strings.NewReader("a: 1\n---\na: 2\n"),
		node,
		printer,
		NewYamlDecoder(ConfiguredYamlPreferences),
	)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	if docs != 0 || printer.calls != 0 {
		t.Fatalf("no document should be printed before stopping: docs=%d calls=%d", docs, printer.calls)
	}
}

func TestStreamEvaluatorStopsAfterResultWhenCancelled(t *testing.T) {
	InitExpressionParser()
	run := newTestRun(t)
	printer := &recordingPrinter{}
	// Cancel after the first PrintResults returns via a wrapper printer.
	wrap := &cancelAfterPrinter{inner: printer, run: run}
	node, err := ExpressionParser.ParseExpression(".")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewStreamEvaluator().Evaluate(
		"file.yml",
		strings.NewReader("a: 1\n---\na: 2\n---\na: 3\n"),
		node,
		wrap,
		NewYamlDecoder(ConfiguredYamlPreferences),
	)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted after first result, got %v", err)
	}
	if printer.calls != 1 {
		t.Fatalf("expected exactly one result printed, got %d", printer.calls)
	}
}

type cancelAfterPrinter struct {
	inner Printer
	run   *Run
	done  bool
}

func (p *cancelAfterPrinter) PrintResults(nodes *list.List) error {
	if err := p.inner.PrintResults(nodes); err != nil {
		return err
	}
	p.run.Cancel()
	return nil
}

func (p *cancelAfterPrinter) PrintedAnything() bool   { return p.inner.PrintedAnything() }
func (p *cancelAfterPrinter) SetAppendix(r io.Reader) { p.inner.SetAppendix(r) }
func (p *cancelAfterPrinter) SetNulSepOutput(b bool)  { p.inner.SetNulSepOutput(b) }

// All-at-once chain: cancellation signalled right after the single print
// returns must surface at the post-print boundary check.
func TestAllAtOnceEvaluatorStopsAfterPrint(t *testing.T) {
	InitExpressionParser()
	run := newTestRun(t)
	path := filepath.Join(t.TempDir(), "a.yml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	printer := &recordingPrinter{}
	wrap := &cancelAfterPrinter{inner: printer, run: run}
	err := NewAllAtOnceEvaluator().EvaluateFiles(".", []string{path}, wrap, NewYamlDecoder(ConfiguredYamlPreferences))
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted after print, got %v", err)
	}
	if printer.calls != 1 {
		t.Fatalf("expected exactly one print, got %d", printer.calls)
	}
}

func TestStreamEvaluatorEvaluateNewCancelled(t *testing.T) {
	InitExpressionParser()
	newTestRun(t).Cancel()
	err := NewStreamEvaluator().EvaluateNew(".a = 1", &recordingPrinter{})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
}

func TestAllAtOnceEvaluatorCancelledAtFileBoundary(t *testing.T) {
	newTestRun(t).Cancel()
	printer := &recordingPrinter{}
	err := NewAllAtOnceEvaluator().EvaluateFiles(".", []string{"a.yml", "b.yml"}, printer, NewYamlDecoder(ConfiguredYamlPreferences))
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	if printer.calls != 0 {
		t.Fatalf("printer must not be invoked, got %d calls", printer.calls)
	}
}

func TestAllAtOnceEvaluatorStopsBeforePrintingWhenCancelled(t *testing.T) {
	run := newTestRun(t)
	dir := t.TempDir()
	path := dir + "/a.yml"
	if err := writeTestFile(path, "a: 1\n"); err != nil {
		t.Fatal(err)
	}
	run.Cancel()
	printer := &recordingPrinter{}
	err := NewAllAtOnceEvaluator().EvaluateFiles(".", []string{path}, printer, NewYamlDecoder(ConfiguredYamlPreferences))
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	if printer.calls != 0 {
		t.Fatalf("print must be skipped, got %d calls", printer.calls)
	}
}

func writeTestFile(path string, content string) error {
	return os.WriteFile(path, []byte(content), 0600)
}
