package yqlib

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikefarah/yq/v4/test"
)

func initParserForTest(t *testing.T) {
	t.Helper()
	InitExpressionParser()
}

func writeTempFile(t *testing.T, name string, contents string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(filename, []byte(contents), 0600); err != nil {
		t.Fatalf("failed to write %v: %v", filename, err)
	}
	return filename
}

func assertSourceFrame(t *testing.T, err error, stage ErrorStage) SourceLocation {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	locations := SourceLocations(err)
	if len(locations) == 0 {
		t.Fatalf("expected a SourceError frame, got %T: %v", err, err)
	}
	if locations[0].Stage != stage {
		t.Fatalf("expected outermost stage %v, got %v", stage, locations[0].Stage)
	}
	return locations[0]
}

func TestReadStreamMissingFileIsDecodeFrame(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yml")
	_, _, err := readStream(missing)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	location := assertSourceFrame(t, err, DecodeStage)
	expectedInText := "bad file '" + missing + "': open " + missing + ":"
	if !strings.Contains(err.Error(), expectedInText) {
		t.Fatalf("expected %q in %q", expectedInText, err.Error())
	}
	if location.Filename != missing {
		t.Fatalf("expected frame filename %v, got %v", missing, location.Filename)
	}

	var pathError *fs.PathError
	if !errors.As(err, &pathError) {
		t.Fatalf("expected errors.As to reach *fs.PathError, got %T: %v", err, err)
	}
}

func TestStreamEvaluateFilesMissingFileIsDecodeFrame(t *testing.T) {
	initParserForTest(t)
	missing := filepath.Join(t.TempDir(), "missing.yml")

	err := NewStreamEvaluator().EvaluateFiles(".", []string{missing}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	location := assertSourceFrame(t, err, DecodeStage)
	expectedInText := "bad file '" + missing + "': open " + missing + ":"
	if !strings.Contains(err.Error(), expectedInText) {
		t.Fatalf("expected %q in %q", expectedInText, err.Error())
	}
	if location.Filename != missing {
		t.Fatalf("expected frame filename %v, got %v", missing, location.Filename)
	}
}

func TestAllAtOnceEvaluateFilesMissingFileIsDecodeFrame(t *testing.T) {
	initParserForTest(t)
	missing := filepath.Join(t.TempDir(), "missing.yml")

	err := NewAllAtOnceEvaluator().EvaluateFiles(".", []string{missing}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	location := assertSourceFrame(t, err, DecodeStage)
	expectedInText := "bad file '" + missing + "': open " + missing + ":"
	if !strings.Contains(err.Error(), expectedInText) {
		t.Fatalf("expected %q in %q", expectedInText, err.Error())
	}
	if location.Filename != missing {
		t.Fatalf("expected frame filename %v, got %v", missing, location.Filename)
	}
}

func TestStreamSingleFileFirstDocumentErrorUnchanged(t *testing.T) {
	initParserForTest(t)
	input := writeTempFile(t, "one.yml", "a: hello\n")

	err := NewStreamEvaluator().EvaluateFiles(`error("boom")`, []string{input}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	test.AssertResult(t, "boom", err.Error())
}

func TestStreamNullInputErrorHasNoSourceFrame(t *testing.T) {
	initParserForTest(t)
	err := NewStreamEvaluator().EvaluateNew(`error("boom")`, discardingPrinter())
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	test.AssertResult(t, "boom", err.Error())
	test.AssertResult(t, 0, len(SourceLocations(err)))
}

func TestStreamLaterDocumentErrorCarriesDocumentIndex(t *testing.T) {
	initParserForTest(t)
	input := writeTempFile(t, "multi.yml", "a: one\n---\na: two\n")

	err := NewStreamEvaluator().EvaluateFiles(`if di == 1 then error("boom") else . end`, []string{input}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	test.AssertResult(t, input+": document index 1: boom", err.Error())
	location := assertSourceFrame(t, err, DocumentStage)
	test.AssertResult(t, input, location.Filename)
	test.AssertResult(t, uint(1), location.DocumentIndex)
}

func TestStreamMultiFileBatchErrorCarriesFilename(t *testing.T) {
	initParserForTest(t)
	first := writeTempFile(t, "first.yml", "a: one\n")
	second := writeTempFile(t, "second.yml", "a: two\n")

	err := NewStreamEvaluator().EvaluateFiles(`if fi == 1 then error("boom") else . end`, []string{first, second}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	test.AssertResult(t, second+": document index 0: boom", err.Error())
	location := assertSourceFrame(t, err, DocumentStage)
	test.AssertResult(t, second, location.Filename)
}

func TestAllAtOnceSingleCandidateErrorIsAttributed(t *testing.T) {
	initParserForTest(t)
	first := writeTempFile(t, "first.yml", "a: one\n")
	second := writeTempFile(t, "second.yml", "a: two\n")

	err := NewAllAtOnceEvaluator().EvaluateFiles(`if fi == 1 then error("boom") else . end`, []string{first, second}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	test.AssertResult(t, second+": document index 0: boom", err.Error())
	assertSourceFrame(t, err, DocumentStage)
}

func TestAllAtOnceAmbiguousContextErrorIsNotAttributed(t *testing.T) {
	initParserForTest(t)
	first := writeTempFile(t, "first.yml", "a: one\n")
	second := writeTempFile(t, "second.yml", "a: two\n")

	err := NewAllAtOnceEvaluator().EvaluateFiles(`error("boom")`, []string{first, second}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	// the context holds candidates from both files: no single source, no frame
	test.AssertResult(t, "boom", err.Error())
	test.AssertResult(t, 0, len(SourceLocations(err)))
}

func TestExpressionSyntaxErrorReportedBeforeInputsOpened(t *testing.T) {
	initParserForTest(t)
	missing := filepath.Join(t.TempDir(), "missing.yml")

	err := NewStreamEvaluator().EvaluateFiles(`...(`, []string{missing}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected a syntax error, got nil")
	}
	if strings.Contains(err.Error(), "bad file") {
		t.Fatalf("expected syntax error without input frame, got %q", err.Error())
	}
	test.AssertResult(t, 0, len(SourceLocations(err)))
}

type failingWriter struct {
	err error
}

func (w *failingWriter) Write(_ []byte) (int, error) {
	return 0, w.err
}

func TestPrinterWriteErrorAttributedToDocument(t *testing.T) {
	initParserForTest(t)
	firstDoc := &CandidateNode{Kind: ScalarNode, Tag: "!!str", Value: "v", filename: "a.yml", document: 0}

	printer := NewPrinter(NewYamlEncoder(ConfiguredYamlPreferences), NewSinglePrinterWriter(bufio.NewWriter(&failingWriter{err: errors.New("disk full")})))
	err := printer.PrintResults(firstDoc.AsList())
	if err == nil {
		t.Fatal("expected a write error, got nil")
	}
	// single file, first document: text is byte-identical to the raw cause
	test.AssertResult(t, "disk full", err.Error())
	assertSourceFrame(t, err, DocumentStage)

	multiPrinter := NewPrinter(NewYamlEncoder(ConfiguredYamlPreferences), NewSinglePrinterWriter(bufio.NewWriter(&failingWriter{err: errors.New("disk full")})))
	multiPrinter.(*resultsPrinter).SetMultiFileBatch(true)
	err = multiPrinter.PrintResults(firstDoc.AsList())
	if err == nil {
		t.Fatal("expected a write error, got nil")
	}
	test.AssertResult(t, "a.yml: document index 0: disk full", err.Error())
}

func TestPrinterExplodeErrorHonoursMultiFileBatch(t *testing.T) {
	// encoders that cannot handle aliases explode merge anchors through the
	// printer's internal navigator, which must share the batch policy
	badMerge := "def:\n  - &x 1\n  - &y 2\na:\n  <<: [*x, *y]\n"
	docs, err := readDocuments(strings.NewReader(badMerge), "mergebad.yml", 0, NewYamlDecoder(ConfiguredYamlPreferences))
	if err != nil {
		t.Fatal(err)
	}

	printer := NewPrinter(NewJSONEncoder(ConfiguredJSONPreferences), NewSinglePrinterWriter(bufio.NewWriter(io.Discard)))
	printer.(*resultsPrinter).SetMultiFileBatch(true)
	err = printer.PrintResults(docs)
	if err == nil {
		t.Fatal("expected an explode error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "mergebad.yml: document index 0: ") {
		t.Fatalf("expected multi-file explode error to carry the source name, got %q", err.Error())
	}
	location := assertSourceFrame(t, err, DocumentStage)
	test.AssertResult(t, "mergebad.yml", location.Filename)
	test.AssertResult(t, uint(0), location.DocumentIndex)

	// single source policy keeps the cause text byte-identical
	singlePrinter := NewPrinter(NewJSONEncoder(ConfiguredJSONPreferences), NewSinglePrinterWriter(bufio.NewWriter(io.Discard)))
	err = singlePrinter.PrintResults(docs)
	if err == nil {
		t.Fatal("expected an explode error, got nil")
	}
	if strings.HasPrefix(err.Error(), "mergebad.yml") {
		t.Fatalf("expected no rendered prefix for a single source, got %q", err.Error())
	}
}

func TestMultiPrinterWriterNamingErrorAttributed(t *testing.T) {
	initParserForTest(t)
	firstDoc := &CandidateNode{Kind: ScalarNode, Tag: "!!str", Value: "v", filename: "input.yml", document: 0}
	laterDoc := &CandidateNode{Kind: ScalarNode, Tag: "!!str", Value: "v", filename: "input.yml", document: 3}
	nameExpression, parseErr := ExpressionParser.ParseExpression(`error("badname")`)
	if parseErr != nil {
		t.Fatal(parseErr)
	}

	writer := NewMultiPrinterWriter(nameExpression, YamlFormat)

	_, err := writer.GetWriter(firstDoc)
	if err == nil {
		t.Fatal("expected a naming error, got nil")
	}
	test.AssertResult(t, "badname", err.Error())

	_, err = writer.GetWriter(laterDoc)
	if err == nil {
		t.Fatal("expected a naming error, got nil")
	}
	test.AssertResult(t, "input.yml: document index 3: badname", err.Error())
	location := assertSourceFrame(t, err, DocumentStage)
	test.AssertResult(t, uint(3), location.DocumentIndex)
}

func TestMultiPrinterWriterTargetCreationErrorAttributed(t *testing.T) {
	initParserForTest(t)
	dir := t.TempDir()
	// a regular file occupying the path that must be a directory
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "out.yml")

	node := &CandidateNode{Kind: ScalarNode, Tag: "!!str", Value: target, filename: "input.yml", document: 0}
	nameExpression, parseErr := ExpressionParser.ParseExpression(".")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	writer := NewMultiPrinterWriter(nameExpression, YamlFormat)

	_, err := writer.GetWriter(node)
	if err == nil {
		t.Fatal("expected a target creation error, got nil")
	}
	locations := SourceLocations(err)
	test.AssertResult(t, 2, len(locations))
	test.AssertResult(t, DocumentStage, locations[0].Stage)
	test.AssertResult(t, SplitOutputStage, locations[1].Stage)
	test.AssertResult(t, target, locations[1].Target)
	if !strings.HasPrefix(err.Error(), "failed to write split output '") || !strings.Contains(err.Error(), target) {
		t.Fatalf("expected split output framing around target %v, got %q", target, err.Error())
	}
}

func TestSplitOutputWriterWriteErrorAttributed(t *testing.T) {
	laterDoc := &CandidateNode{Kind: ScalarNode, Tag: "!!str", Value: "v", filename: "input.yml", document: 2}
	writer := &splitOutputWriter{file: nil, target: "out.yml", candidate: laterDoc}

	_, err := writer.Write([]byte("hi"))
	if err == nil {
		t.Fatal("expected a write error, got nil")
	}
	if !strings.Contains(err.Error(), "input.yml: document index 2: failed to write split output 'out.yml':") {
		t.Fatalf("expected document and split output frames, got %q", err.Error())
	}
	locations := SourceLocations(err)
	test.AssertResult(t, DocumentStage, locations[0].Stage)
	test.AssertResult(t, SplitOutputStage, locations[1].Stage)
}

func TestStringEvaluatorLaterDocumentErrorCarriesIndex(t *testing.T) {
	initParserForTest(t)
	input := "a: one\n---\na: two\n"

	_, err := NewStringEvaluator().Evaluate(`if di == 1 then error(.missing) else . end`, input, NewYamlEncoder(ConfiguredYamlPreferences), NewYamlDecoder(ConfiguredYamlPreferences))
	if err == nil {
		t.Fatal("expected a string evaluator error, got nil")
	}
	test.AssertResult(t, "document index 1: aborted", err.Error())
	assertSourceFrame(t, err, DocumentStage)
}

func TestInterpolationSingleFileFirstDocumentErrorUnchanged(t *testing.T) {
	initParserForTest(t)
	input := writeTempFile(t, "one.yml", "a: hello\n")

	err := NewStreamEvaluator().EvaluateFiles(`"prefix \(error(.missing))"`, []string{input}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected an interpolation error, got nil")
	}
	test.AssertResult(t, "aborted", err.Error())
}

func TestInterpolationLaterDocumentErrorCarriesDocumentIndex(t *testing.T) {
	initParserForTest(t)
	input := writeTempFile(t, "multi.yml", "a: one\n---\na: two\n")
	expression := `"\(if di == 1 then error(.missing) else .a end)"`

	err := NewStreamEvaluator().EvaluateFiles(expression, []string{input}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected an interpolation error, got nil")
	}
	test.AssertResult(t, input+": document index 1: aborted", err.Error())
	assertSourceFrame(t, err, DocumentStage)
}

func TestInterpolationNullInputErrorHasNoFrame(t *testing.T) {
	initParserForTest(t)
	err := NewStreamEvaluator().EvaluateNew(`"x \(error(.missing))"`, discardingPrinter())
	if err == nil {
		t.Fatal("expected an interpolation error, got nil")
	}
	test.AssertResult(t, "aborted", err.Error())
	test.AssertResult(t, 0, len(SourceLocations(err)))
}

func TestInterpolationInnerParseErrorIsAttributed(t *testing.T) {
	initParserForTest(t)
	input := writeTempFile(t, "one.yml", "a: hello\n")

	err := NewStreamEvaluator().EvaluateFiles(`"x \(1 +)"`, []string{input}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))
	if err == nil {
		t.Fatal("expected an interpolation parse error, got nil")
	}
	// first document of a single file: the prefix renders empty, text stays the parser message
	if strings.HasPrefix(err.Error(), "document index") {
		t.Fatalf("expected no rendered prefix on first document, got %q", err.Error())
	}
	assertSourceFrame(t, err, DocumentStage)

	nullErr := NewStreamEvaluator().EvaluateNew(`"x \(1 +)"`, discardingPrinter())
	if nullErr == nil {
		t.Fatal("expected an interpolation parse error, got nil")
	}
	test.AssertResult(t, 0, len(SourceLocations(nullErr)))
}

func TestLoadChainSingleFileKeepsInnerFrameOnly(t *testing.T) {
	initParserForTest(t)
	outer := writeTempFile(t, "outer.yml", "a: 1\n")
	inner := filepath.Join(t.TempDir(), "cat.yml")

	err := NewStreamEvaluator().EvaluateFiles(`load("`+inner+`")`, []string{outer}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected a load error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "failed to load "+inner+": open "+inner+":") {
		t.Fatalf("expected frozen single file load text, got %q", err.Error())
	}
	locations := SourceLocations(err)
	test.AssertResult(t, DocumentStage, locations[0].Stage)
	test.AssertResult(t, outer, locations[0].Filename)
	test.AssertResult(t, LoadStage, locations[1].Stage)
	test.AssertResult(t, inner, locations[1].Target)
	var pathError *fs.PathError
	test.AssertResult(t, true, errors.As(err, &pathError))
}

func TestLoadChainMultiFileShowsOuterAndInner(t *testing.T) {
	initParserForTest(t)
	first := writeTempFile(t, "first.yml", "a: 1\n")
	second := writeTempFile(t, "second.yml", "a: 2\n")
	inner := filepath.Join(t.TempDir(), "cat.yml")
	expression := `if fi == 1 then load("` + inner + `") else . end`

	err := NewStreamEvaluator().EvaluateFiles(expression, []string{first, second}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected a load error, got nil")
	}
	if !strings.HasPrefix(err.Error(), second+": document index 0: failed to load "+inner+":") {
		t.Fatalf("expected outer input and inner file on the chain, got %q", err.Error())
	}
	locations := SourceLocations(err)
	test.AssertResult(t, second, locations[0].Filename)
	test.AssertResult(t, inner, locations[1].Target)
}

func TestLoadChainNullInputTextUnchanged(t *testing.T) {
	initParserForTest(t)
	err := NewStreamEvaluator().EvaluateNew(`load("cat.yml")`, discardingPrinter())
	if err == nil {
		t.Fatal("expected a load error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "failed to load cat.yml: open cat.yml:") {
		t.Fatalf("expected frozen -n load text, got %q", err.Error())
	}
	locations := SourceLocations(err)
	// null input contributes no document frame, only the load frame
	test.AssertResult(t, LoadStage, locations[0].Stage)
}

func TestLoadChainInnerBadDecodeStaysNested(t *testing.T) {
	initParserForTest(t)
	outer := writeTempFile(t, "outer.yml", "a: 1\n")
	inner := writeTempFile(t, "inner.yml", "a: b\n- nope\n")

	err := NewStreamEvaluator().EvaluateFiles(`load("`+inner+`")`, []string{outer}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected a load decode error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to load "+inner+": bad file '"+inner+"':") {
		t.Fatalf("expected nested load/decode frames, got %q", err.Error())
	}
	locations := SourceLocations(err)
	test.AssertResult(t, LoadStage, locations[1].Stage)
	test.AssertResult(t, DecodeStage, locations[2].Stage)
}

func TestDecodeErrorTextAndLineNumbersPreserved(t *testing.T) {
	initParserForTest(t)
	badYaml := writeTempFile(t, "bad.yml", "a: b\n- nope\n")

	err := NewStreamEvaluator().EvaluateFiles(".", []string{badYaml}, discardingPrinter(), NewYamlDecoder(ConfiguredYamlPreferences))

	if err == nil {
		t.Fatal("expected a decode error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "bad file '"+badYaml+"': ") {
		t.Fatalf("expected frozen bad file prefix, got %q", err.Error())
	}
	// the decoder produced cause (including the line/column marker) must be preserved verbatim
	if !strings.Contains(err.Error(), "L2.C1") || !strings.Contains(err.Error(), "did not find expected key") {
		t.Fatalf("expected decoder cause and line marker to be preserved, got %q", err.Error())
	}
	assertSourceFrame(t, err, DecodeStage)
}
