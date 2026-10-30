package yqlib

import (
	"bufio"
	"bytes"
	"container/list"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decodeOneNode(t *testing.T, content string) *CandidateNode {
	t.Helper()
	decoder := NewYamlDecoder(ConfiguredYamlPreferences)
	if err := decoder.Init(strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	node, err := decoder.Decode()
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func nodeList(nodes ...*CandidateNode) *list.List {
	l := list.New()
	for _, n := range nodes {
		l.PushBack(n)
	}
	return l
}

func TestMultiPrinterWriterRefusesWhenCancelled(t *testing.T) {
	InitExpressionParser()
	run := newTestRun(t)
	dir := t.TempDir()
	expr, err := ExpressionParser.ParseExpression(`"` + filepath.ToSlash(dir) + `/" + .name`)
	if err != nil {
		t.Fatal(err)
	}
	mw := NewMultiPrinterWriter(expr, YamlFormat)
	node := decodeOneNode(t, "name: thing\n")

	run.Cancel()
	if _, err := mw.GetWriter(node); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted before opening file, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "thing.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no split file may be created after cancel: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("no directories may be created after cancel, got %v", entries)
	}
}

// cancellingWriter cancels after the first successful GetWriter.
type cancellingWriter struct {
	inner PrinterWriter
	run   *Run
	count int
}

func (c *cancellingWriter) GetWriter(node *CandidateNode) (*bufio.Writer, error) {
	w, err := c.inner.GetWriter(node)
	if err != nil {
		return nil, err
	}
	c.count++
	if c.count == 1 {
		c.run.Cancel()
	}
	return w, nil
}

func TestPrinterKeepsCompleteAndDropsPartialSplit(t *testing.T) {
	InitExpressionParser()
	run := newTestRun(t)
	dir := t.TempDir()
	expr, err := ExpressionParser.ParseExpression(`"` + filepath.ToSlash(dir) + `/" + .name`)
	if err != nil {
		t.Fatal(err)
	}
	inner := NewMultiPrinterWriter(expr, YamlFormat)
	cw := &cancellingWriter{inner: inner, run: run}
	printer := NewPrinter(NewYamlEncoder(ConfiguredYamlPreferences), cw)

	err = printer.PrintResults(nodeList(
		decodeOneNode(t, "name: one\n"),
		decodeOneNode(t, "name: two\n"),
	))
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted between results, got %v", err)
	}
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}

	one := filepath.Join(dir, "one.yml")
	two := filepath.Join(dir, "two.yml")
	if _, err := os.Stat(one); err != nil {
		t.Fatalf("complete split file must be kept: %v", err)
	}
	if _, err := os.Stat(two); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled split file must never be created")
	}
	// Handle must be closed: delete must succeed.
	if err := os.Remove(one); err != nil {
		t.Fatalf("complete split file handle not closed: %v", err)
	}
}

func TestPrinterSkipsAppendixAfterCancel(t *testing.T) {
	run := newTestRun(t)
	buf := &bytes.Buffer{}
	printer := NewPrinter(NewYamlEncoder(ConfiguredYamlPreferences), NewSinglePrinterWriter(buf))
	printer.SetAppendix(strings.NewReader("APPENDIX"))

	run.Cancel()
	err := printer.PrintResults(nodeList(decodeOneNode(t, "a: 1\n")))
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	if _, err := run.Finalise(false); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("nothing should be written after cancel, got %q", buf.String())
	}
}

var errBoomWrite = errors.New("simulated downstream closed")

type failingWriter struct {
	writes int
}

func (f *failingWriter) Write(_ []byte) (int, error) {
	f.writes++
	return 0, errBoomWrite
}

func TestPrinterWriteErrorIsNotInterruption(t *testing.T) {
	newTestRun(t) // active run, but never cancelled
	fw := &failingWriter{}
	printer := NewPrinter(NewYamlEncoder(ConfiguredYamlPreferences), NewSinglePrinterWriter(fw))

	err := printer.PrintResults(nodeList(decodeOneNode(t, "a: 1\n")))
	if err == nil {
		t.Fatal("expected write error")
	}
	if errors.Is(err, ErrInterrupted) {
		t.Fatalf("write error must not become ErrInterrupted: %v", err)
	}
	if !errors.Is(err, errBoomWrite) {
		t.Fatalf("original write error must be returned verbatim, got %v", err)
	}
}

func TestSinglePrinterWriterFlushedOnFinalise(t *testing.T) {
	run := newTestRun(t)
	buf := &bytes.Buffer{}
	spw := NewSinglePrinterWriter(buf)
	w, err := spw.GetWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("tail-content"); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatal("writer must stay buffered until finalise")
	}
	if _, err := run.Finalise(true); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "tail-content" {
		t.Fatalf("buffered output lost: %q", buf.String())
	}
}
