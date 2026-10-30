package yqlib

import (
	"bufio"
	"bytes"
	"container/list"
	"fmt"
	"io"
	"regexp"
)

type Printer interface {
	PrintResults(matchingNodes *list.List) error
	PrintedAnything() bool
	//e.g. when given a front-matter doc, like jekyll
	SetAppendix(reader io.Reader)
	SetNulSepOutput(nulSepOutput bool)
}

type resultsPrinter struct {
	encoder           Encoder
	printerWriter     PrinterWriter
	firstTimePrinting bool
	previousDocIndex  uint
	previousFileIndex int
	printedMatches    bool
	treeNavigator     DataTreeNavigator
	appendixReader    io.Reader
	nulSepOutput      bool
	multiFileBatch    bool
}

func NewPrinter(encoder Encoder, printerWriter PrinterWriter) Printer {
	return &resultsPrinter{
		encoder:           encoder,
		printerWriter:     printerWriter,
		firstTimePrinting: true,
		treeNavigator:     NewDataTreeNavigator(),
		nulSepOutput:      false,
	}
}

func (p *resultsPrinter) SetNulSepOutput(nulSepOutput bool) {
	log.Debug("Setting NUL separator output")

	p.nulSepOutput = nulSepOutput
}

// SetMultiFileBatch records that the printer is handling a batch of multiple
// input files, so write errors attributed to the first document render the
// source filename. The policy is also pushed to the internal navigator used
// when exploding aliases for encoders that cannot handle them.
func (p *resultsPrinter) SetMultiFileBatch(multiFileBatch bool) {
	p.multiFileBatch = multiFileBatch
	if navigator, ok := p.treeNavigator.(*dataTreeNavigator); ok {
		navigator.multiFileBatch = multiFileBatch
	}
}

func (p *resultsPrinter) withDocumentError(err error, node *CandidateNode) error {
	return newDocumentErrorForCandidate(err, node, p.multiFileBatch)
}

func (p *resultsPrinter) SetAppendix(reader io.Reader) {
	p.appendixReader = reader
}

func (p *resultsPrinter) PrintedAnything() bool {
	return p.printedMatches
}

func (p *resultsPrinter) printNode(node *CandidateNode, writer io.Writer) error {
	p.printedMatches = p.printedMatches || (node.Tag != "!!null" &&
		(node.Tag != "!!bool" || node.Value != "false"))
	return p.encoder.Encode(writer, node)
}

func removeLastEOL(b *bytes.Buffer) {
	data := b.Bytes()
	n := len(data)
	if n >= 2 && data[n-2] == '\r' && data[n-1] == '\n' {
		b.Truncate(n - 2)
	} else if n >= 1 && (data[n-1] == '\r' || data[n-1] == '\n') {
		b.Truncate(n - 1)
	}
}

func (p *resultsPrinter) PrintResults(matchingNodes *list.List) error {
	log.Debugf("PrintResults for %v matches", matchingNodes.Len())

	if matchingNodes.Len() == 0 {
		log.Debug("no matching results, nothing to print")
		return nil
	}

	if !p.encoder.CanHandleAliases() {
		explodeOp := Operation{OperationType: explodeOpType}
		explodeNode := ExpressionNode{Operation: &explodeOp}
		context, err := p.treeNavigator.GetMatchingNodes(Context{MatchingNodes: matchingNodes}, &explodeNode)
		if err != nil {
			if matchingNodes.Len() == 1 {
				return p.withDocumentError(err, matchingNodes.Front().Value.(*CandidateNode))
			}
			return err
		}
		matchingNodes = context.MatchingNodes
	}

	if p.firstTimePrinting {
		node := matchingNodes.Front().Value.(*CandidateNode)
		p.previousDocIndex = node.GetDocument()
		p.previousFileIndex = node.GetFileIndex()
		p.firstTimePrinting = false
	}

	for el := matchingNodes.Front(); el != nil; el = el.Next() {

		mappedDoc := el.Value.(*CandidateNode)
		log.Debugf("print sep logic: p.firstTimePrinting: %v, previousDocIndex: %v", p.firstTimePrinting, p.previousDocIndex)
		log.Debugf("%v", NodeToString(mappedDoc))
		writer, errorWriting := p.printerWriter.GetWriter(mappedDoc)
		if errorWriting != nil {
			return p.withDocumentError(errorWriting, mappedDoc)
		}

		commentsStartWithSepExp := regexp.MustCompile(`^\$yqDocSeparator\$`)
		commentStartsWithSeparator := commentsStartWithSepExp.MatchString(mappedDoc.LeadingContent)

		if (p.previousDocIndex != mappedDoc.GetDocument() || p.previousFileIndex != mappedDoc.GetFileIndex()) && !commentStartsWithSeparator {
			if err := p.encoder.PrintDocumentSeparator(writer); err != nil {
				return p.withDocumentError(err, mappedDoc)
			}
		}

		var destination io.Writer = writer
		tempBuffer := bytes.NewBuffer(nil)
		if p.nulSepOutput {
			destination = tempBuffer
		}

		if err := p.encoder.PrintLeadingContent(destination, mappedDoc.LeadingContent); err != nil {
			return p.withDocumentError(err, mappedDoc)
		}

		if err := p.printNode(mappedDoc, destination); err != nil {
			return p.withDocumentError(err, mappedDoc)
		}

		if p.nulSepOutput {
			removeLastEOL(tempBuffer)
			tempBufferBytes := tempBuffer.Bytes()
			if bytes.IndexByte(tempBufferBytes, 0) != -1 {
				return p.withDocumentError(fmt.Errorf(
					"can't serialise value because it contains NUL char and you are using NUL separated output",
				), mappedDoc)
			}
			if _, err := writer.Write(tempBufferBytes); err != nil {
				return p.withDocumentError(err, mappedDoc)
			}
			if _, err := writer.Write([]byte{0}); err != nil {
				return p.withDocumentError(err, mappedDoc)
			}
		}

		p.previousDocIndex = mappedDoc.GetDocument()
		if err := writer.Flush(); err != nil {
			return p.withDocumentError(err, mappedDoc)
		}
		log.Debugf("done printing results")
	}

	// what happens if I remove output format check?
	if p.appendixReader != nil {
		writer, err := p.printerWriter.GetWriter(nil)
		if err != nil {
			return err
		}

		log.Debug("Piping appendix reader...")
		betterReader := bufio.NewReader(p.appendixReader)
		_, err = io.Copy(writer, betterReader)
		if err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}

	return nil
}
