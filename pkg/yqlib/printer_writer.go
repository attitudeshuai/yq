package yqlib

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

type PrinterWriter interface {
	GetWriter(node *CandidateNode) (*bufio.Writer, error)
}

type singlePrinterWriter struct {
	bufferedWriter *bufio.Writer
}

func NewSinglePrinterWriter(writer io.Writer) PrinterWriter {
	return &singlePrinterWriter{
		bufferedWriter: bufio.NewWriter(writer),
	}
}

func (sp *singlePrinterWriter) GetWriter(_ *CandidateNode) (*bufio.Writer, error) {
	return sp.bufferedWriter, nil
}

type multiPrintWriter struct {
	treeNavigator  DataTreeNavigator
	nameExpression *ExpressionNode
	extension      string
	index          int
	multiFileBatch bool
}

// splitOutputWriter annotates write errors to a split-output target with the
// target name and the source document they originated from.
type splitOutputWriter struct {
	file           *os.File
	target         string
	candidate      *CandidateNode
	multiFileBatch bool
}

func (w *splitOutputWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	if err != nil {
		return n, newDocumentErrorForCandidate(newSplitOutputError(err, w.target), w.candidate, w.multiFileBatch)
	}
	return n, nil
}

func NewMultiPrinterWriter(expression *ExpressionNode, format *Format) PrinterWriter {
	extension := "yml"

	switch format {
	case JSONFormat:
		extension = "json"
	case PropertiesFormat:
		extension = "properties"
	}

	return &multiPrintWriter{
		nameExpression: expression,
		extension:      extension,
		treeNavigator:  NewDataTreeNavigator(),
		index:          0,
	}
}

// SetMultiFileBatch records that split output is driven by a batch of multiple
// input files, so naming and write errors attributed to the first document
// render the source filename. The policy is also pushed to the internal
// navigator used to evaluate the naming expression.
func (sp *multiPrintWriter) SetMultiFileBatch(multiFileBatch bool) {
	sp.multiFileBatch = multiFileBatch
	if navigator, ok := sp.treeNavigator.(*dataTreeNavigator); ok {
		navigator.multiFileBatch = multiFileBatch
	}
}

func (sp *multiPrintWriter) annotateTargetError(err error, name string, node *CandidateNode) error {
	err = newSplitOutputError(err, name)
	return newDocumentErrorForCandidate(err, node, sp.multiFileBatch)
}

func (sp *multiPrintWriter) GetWriter(node *CandidateNode) (*bufio.Writer, error) {
	name := ""

	indexVariableNode := CandidateNode{Kind: ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%v", sp.index)}

	context := Context{MatchingNodes: node.AsList()}
	context.SetVariable("index", indexVariableNode.AsList())
	result, err := sp.treeNavigator.GetMatchingNodes(context, sp.nameExpression)
	if err != nil {
		// naming expression failures are attributed to the source document by
		// the navigator chokepoint (single candidate context above)
		return nil, err
	}
	if result.MatchingNodes.Len() > 0 {
		name = result.MatchingNodes.Front().Value.(*CandidateNode).Value
	}
	var extensionRegexp = regexp.MustCompile(`\.[a-zA-Z0-9]+$`)
	if !extensionRegexp.MatchString(name) {
		name = fmt.Sprintf("%v.%v", name, sp.extension)
	}

	err = os.MkdirAll(filepath.Dir(name), 0750)
	if err != nil {
		return nil, sp.annotateTargetError(err, name, node)
	}
	f, err := os.Create(name)

	if err != nil {
		return nil, sp.annotateTargetError(err, name, node)
	}
	sp.index = sp.index + 1

	return bufio.NewWriter(&splitOutputWriter{
		file:           f,
		target:         name,
		candidate:      node,
		multiFileBatch: sp.multiFileBatch,
	}), nil

}
