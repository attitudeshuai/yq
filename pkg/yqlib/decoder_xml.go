//go:build !yq_noxml

package yqlib

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html/charset"
)

type xmlDecoder struct {
	reader       io.Reader
	readAnything bool
	finished     bool
	prefs        XmlPreferences
	// chardataForms records, in document order, whether each
	// encoding/xml CharData token came from a CDATA section (true) or
	// plain text (false). Only populated when CDATA preservation is on.
	chardataForms []bool
	chardataIndex int
}

func NewXMLDecoder(prefs XmlPreferences) Decoder {
	return &xmlDecoder{
		finished: false,
		prefs:    prefs,
	}
}

func (dec *xmlDecoder) Init(reader io.Reader) error {
	dec.reader = reader
	dec.readAnything = false
	dec.finished = false
	dec.chardataForms = nil
	dec.chardataIndex = 0
	return nil
}

func (dec *xmlDecoder) createSequence(nodes []*xmlNode) (*CandidateNode, error) {
	yamlNode := &CandidateNode{Kind: SequenceNode, Tag: "!!seq"}
	for _, child := range nodes {
		yamlChild, err := dec.convertToYamlNode(child)
		if err != nil {
			return nil, err
		}
		yamlNode.AddChild(yamlChild)
	}

	return yamlNode, nil
}

var decoderCommentPrefix = regexp.MustCompile(`(^|\n)([[:alpha:]])`)

func (dec *xmlDecoder) processComment(c string) string {
	if c == "" {
		return ""
	}
	//need to replace "cat " with "# cat"
	// "\ncat\n" with "\n cat\n"
	// ensure non-empty comments starting with newline have a space in front

	replacement := decoderCommentPrefix.ReplaceAllString(c, "$1 $2")
	replacement = "#" + strings.ReplaceAll(strings.TrimRight(replacement, " "), "\n", "\n#")
	return replacement
}

func (dec *xmlDecoder) createMap(n *xmlNode) (*CandidateNode, error) {
	log.Debugf("createMap: headC: %v, lineC: %v, footC: %v", n.HeadComment, n.LineComment, n.FootComment)
	yamlNode := &CandidateNode{Kind: MappingNode, Tag: "!!map"}

	if len(n.Data) > 0 {
		log.Debugf("creating content node for map: %v", dec.prefs.ContentName)
		label := dec.prefs.ContentName
		labelNode := createScalarNode(label, label)
		labelNode.HeadComment = dec.processComment(n.HeadComment)
		labelNode.LineComment = dec.processComment(n.LineComment)
		labelNode.FootComment = dec.processComment(n.FootComment)
		yamlNode.AddKeyValueChild(labelNode, dec.createValueNodeFromData(n.Data, n.DataForms))
	}

	for i, keyValuePair := range n.Children {
		label := keyValuePair.K
		children := keyValuePair.V
		labelNode := createScalarNode(label, label)
		var valueNode *CandidateNode
		var err error

		if i == 0 {
			log.Debugf("head comment here")
			labelNode.HeadComment = dec.processComment(n.HeadComment)

		}
		log.Debugf("label=%v, i=%v, keyValuePair.FootComment: %v", label, i, keyValuePair.FootComment)
		labelNode.FootComment = dec.processComment(keyValuePair.FootComment)

		log.Debugf("len of children in %v is %v", label, len(children))
		if len(children) > 1 {
			valueNode, err = dec.createSequence(children)
			if err != nil {
				return nil, err
			}
		} else {
			// comment hack for maps of scalars
			// if the value is a scalar, the head comment of the scalar needs to go on the key?
			// add tests for <z/> as well as multiple <ds> of inputXmlWithComments > yaml
			if len(children[0].Children) == 0 && children[0].HeadComment != "" {
				if len(children[0].Data) > 0 {

					log.Debugf("scalar comment hack, currentlabel [%v]", labelNode.HeadComment)
					labelNode.HeadComment = joinComments([]string{labelNode.HeadComment, strings.TrimSpace(children[0].HeadComment)}, "\n")
					children[0].HeadComment = ""
				} else {
					// child is null, put the headComment as a linecomment for reasons
					children[0].LineComment = children[0].HeadComment
					children[0].HeadComment = ""
				}
			}
			valueNode, err = dec.convertToYamlNode(children[0])
			if err != nil {
				return nil, err
			}
		}
		yamlNode.AddKeyValueChild(labelNode, valueNode)
	}

	return yamlNode, nil
}

func (dec *xmlDecoder) createCharDataScalar(value string, isCData bool) *CandidateNode {
	node := createScalarNode(value, value)
	if isCData {
		node.Style = CdataStyle
		node.SourceValue = value
	}
	return node
}

func (dec *xmlDecoder) isCDataSegment(forms []bool, index int) bool {
	return dec.prefs.KeepCData && index < len(forms) && forms[index]
}

func (dec *xmlDecoder) createValueNodeFromData(values []string, forms []bool) *CandidateNode {
	switch len(values) {
	case 0:
		return createScalarNode(nil, "")
	case 1:
		return dec.createCharDataScalar(values[0], dec.isCDataSegment(forms, 0))
	default:
		content := make([]*CandidateNode, 0)
		for i, value := range values {
			content = append(content, dec.createCharDataScalar(value, dec.isCDataSegment(forms, i)))
		}
		seq := &CandidateNode{
			Kind:    SequenceNode,
			Tag:     "!!seq",
			Content: content,
		}
		if dec.prefs.KeepCData {
			// These are character data segments of a single element,
			// not an array of repeated elements. Mark the sequence so
			// the XML encoder writes them back in one element.
			seq.Style = CdataStyle
		}
		return seq
	}
}

func (dec *xmlDecoder) convertToYamlNode(n *xmlNode) (*CandidateNode, error) {
	if len(n.Children) > 0 {
		return dec.createMap(n)
	}

	scalar := dec.createValueNodeFromData(n.Data, n.DataForms)

	log.Debugf("scalar (%v), headC: %v, lineC: %v, footC: %v", scalar.Tag, n.HeadComment, n.LineComment, n.FootComment)
	scalar.HeadComment = dec.processComment(n.HeadComment)
	scalar.LineComment = dec.processComment(n.LineComment)
	if scalar.Tag == "!!seq" {
		scalar.Content[0].HeadComment = scalar.LineComment
		scalar.LineComment = ""
	}

	scalar.FootComment = dec.processComment(n.FootComment)

	return scalar, nil
}

func (dec *xmlDecoder) Decode() (*CandidateNode, error) {
	if dec.finished {
		return nil, io.EOF
	}
	root := &xmlNode{}
	// cant use xj - it doesn't keep map order.
	err := dec.decodeXML(root)

	if err != nil {
		return nil, err
	}
	firstNode, err := dec.convertToYamlNode(root)

	if err != nil {
		return nil, err
	} else if firstNode.Tag == "!!null" {
		dec.finished = true
		if dec.readAnything {
			return nil, io.EOF
		}
	}
	dec.readAnything = true
	dec.finished = true

	return firstNode, nil
}

type xmlNode struct {
	Children    []*xmlChildrenKv
	HeadComment string
	FootComment string
	LineComment string
	Data        []string
	// DataForms records, in parallel with Data, whether that character
	// data segment came from a CDATA section (true) or plain text
	// (false). Only populated when CDATA preservation is enabled.
	DataForms []bool
}

type xmlChildrenKv struct {
	K           string
	V           []*xmlNode
	FootComment string
}

// AddChild appends a node to the list of children
func (n *xmlNode) AddChild(s string, c *xmlNode) {

	if n.Children == nil {
		n.Children = make([]*xmlChildrenKv, 0)
	}
	log.Debugf("looking for %s", s)
	// see if we can find an existing entry to add to
	for _, childEntry := range n.Children {
		if childEntry.K == s {
			log.Debugf("found it, appending an entry%s", s)
			childEntry.V = append(childEntry.V, c)
			log.Debugf("yay len of children in %v is %v", s, len(childEntry.V))
			return
		}
	}
	log.Debugf("not there, making a new one %s", s)
	n.Children = append(n.Children, &xmlChildrenKv{K: s, V: []*xmlNode{c}})
}

type element struct {
	parent *element
	n      *xmlNode
	label  string
	state  string
}

// nextChardataForm returns the source form (true for a CDATA section,
// false for plain text) of the next CharData token, consuming it so
// the forms stay aligned with encoding/xml's token order.
func (dec *xmlDecoder) nextChardataForm() bool {
	if dec.chardataIndex >= len(dec.chardataForms) {
		return false
	}
	form := dec.chardataForms[dec.chardataIndex]
	dec.chardataIndex++
	return form
}

// this code is heavily based on https://github.com/basgys/goxml2json
// main changes are to decode into a structure that preserves the original order
// of the map keys.
func (dec *xmlDecoder) decodeXML(root *xmlNode) error {
	reader := dec.reader
	if dec.prefs.KeepCData {
		// encoding/xml hands back both plain text and CDATA sections as
		// indistinguishable CharData tokens, so scan the raw input once
		// to record each token's source form in document order.
		data, err := io.ReadAll(dec.reader)
		if err != nil {
			return err
		}
		dec.chardataForms = scanXMLChardataForms(data)
		dec.chardataIndex = 0
		reader = bytes.NewReader(data)
	}

	xmlDec := xml.NewDecoder(reader)
	xmlDec.Strict = dec.prefs.StrictMode
	// That will convert the charset if the provided XML is non-UTF-8
	xmlDec.CharsetReader = charset.NewReaderLabel

	started := false

	// Create first element from the root node
	elem := &element{
		parent: nil,
		n:      root,
	}

	getToken := func() (xml.Token, error) {
		if dec.prefs.UseRawToken {
			return xmlDec.RawToken()
		}
		return xmlDec.Token()
	}

	for {
		t, e := getToken()
		if e != nil && !errors.Is(e, io.EOF) {
			return e
		}
		if t == nil {
			break
		}

		switch se := t.(type) {
		case xml.StartElement:
			log.Debugf("start element %v", se.Name.Local)
			elem.state = "started"
			// Build new a new current element and link it to its parent
			var label = se.Name.Local
			if dec.prefs.KeepNamespace {
				if se.Name.Space != "" {
					label = se.Name.Space + ":" + se.Name.Local
				}
			}
			elem = &element{
				parent: elem,
				n:      &xmlNode{},
				label:  label,
			}

			// Extract attributes as children
			for _, a := range se.Attr {
				if dec.prefs.KeepNamespace {
					if a.Name.Space != "" {
						a.Name.Local = a.Name.Space + ":" + a.Name.Local
					}
				}
				elem.n.AddChild(dec.prefs.AttributePrefix+a.Name.Local, &xmlNode{Data: []string{a.Value}})
			}
		case xml.CharData:

			// form must be consumed per token to stay aligned with the
			// scanner, even when the trimmed segment is discarded below.
			isCData := false
			if dec.prefs.KeepCData {
				isCData = dec.nextChardataForm()
			}

			// Extract XML data (if any)
			newBit := trimNonGraphic(string(se))
			if !started && len(newBit) > 0 {
				return fmt.Errorf("invalid XML: Encountered chardata [%v] outside of XML node", newBit)
			}

			if len(newBit) > 0 {
				elem.n.Data = append(elem.n.Data, newBit)
				if dec.prefs.KeepCData {
					elem.n.DataForms = append(elem.n.DataForms, isCData)
				}
				elem.state = "chardata"
				log.Debugf("chardata [%v] for %v", elem.n.Data, elem.label)
			}
		case xml.EndElement:
			if elem == nil {
				log.Debug("no element, probably bad xml")
				continue
			}
			log.Debugf("end element %v", elem.label)
			elem.state = "finished"
			// And add it to its parent list
			if elem.parent != nil {
				elem.parent.n.AddChild(elem.label, elem.n)
			}

			// Then change the current element to its parent
			elem = elem.parent
		case xml.Comment:

			commentStr := string(xml.CharData(se))
			switch elem.state {
			case "started":
				applyFootComment(elem, commentStr)

			case "chardata":
				log.Debugf("got a line comment for (%v) %v: [%v]", elem.state, elem.label, commentStr)
				elem.n.LineComment = joinComments([]string{elem.n.LineComment, commentStr}, " ")
			default:
				log.Debugf("got a head comment for (%v) %v: [%v]", elem.state, elem.label, commentStr)
				elem.n.HeadComment = joinComments([]string{elem.n.HeadComment, commentStr}, " ")
			}

		case xml.ProcInst:
			if !dec.prefs.SkipProcInst {
				elem.n.AddChild(dec.prefs.ProcInstPrefix+se.Target, &xmlNode{Data: []string{string(se.Inst)}})
			}
		case xml.Directive:
			if !dec.prefs.SkipDirectives {
				elem.n.AddChild(dec.prefs.DirectiveName, &xmlNode{Data: []string{string(se)}})
			}
		}
		started = true
	}

	return nil
}

func applyFootComment(elem *element, commentStr string) {

	// first lets try to put the comment on the last child
	if len(elem.n.Children) > 0 {
		lastChildIndex := len(elem.n.Children) - 1
		childKv := elem.n.Children[lastChildIndex]
		log.Debugf("got a foot comment, putting on last child for %v: [%v]", childKv.K, commentStr)
		// if it's an array of scalars, put the foot comment on the scalar itself
		if len(childKv.V) > 0 && len(childKv.V[0].Children) == 0 {
			nodeToUpdate := childKv.V[len(childKv.V)-1]
			nodeToUpdate.FootComment = joinComments([]string{nodeToUpdate.FootComment, commentStr}, " ")
		} else {
			childKv.FootComment = joinComments([]string{elem.n.FootComment, commentStr}, " ")
		}
	} else {
		log.Debugf("got a foot comment for %v: [%v]", elem.label, commentStr)
		elem.n.FootComment = joinComments([]string{elem.n.FootComment, commentStr}, " ")
	}
}

func joinComments(rawStrings []string, joinStr string) string {
	stringsToJoin := make([]string, 0)
	for _, str := range rawStrings {
		if str != "" {
			stringsToJoin = append(stringsToJoin, str)
		}
	}
	return strings.Join(stringsToJoin, joinStr)
}

// trimNonGraphic returns a slice of the string s, with all leading and trailing
// non graphic characters and spaces removed.
//
// Graphic characters include letters, marks, numbers, punctuation, symbols,
// and spaces, from categories L, M, N, P, S, Zs.
// Spacing characters are set by category Z and property Pattern_White_Space.
func trimNonGraphic(s string) string {
	if s == "" {
		return s
	}

	var first *int
	var last int
	for i, r := range []rune(s) {
		if !unicode.IsGraphic(r) || unicode.IsSpace(r) {
			continue
		}

		if first == nil {
			f := i // copy i
			first = &f
			last = i
		} else {
			last = i
		}
	}

	// If first is nil, it means there are no graphic characters
	if first == nil {
		return ""
	}

	return string([]rune(s)[*first : last+1])
}

var (
	cdataSectionStart = []byte("<![CDATA[")
	cdataSectionEnd   = []byte("]]>")
	xmlCommentStart   = []byte("<!--")
	xmlCommentEnd     = []byte("-->")
	xmlProcInstEnd    = []byte("?>")
)

func indexAfter(data []byte, start int, sep []byte) int {
	if start >= len(data) {
		return -1
	}
	idx := bytes.Index(data[start:], sep)
	if idx < 0 {
		return -1
	}
	return start + idx
}

// scanXMLChardataForms walks the raw XML bytes and records, in document
// order, whether each character data token emitted by encoding/xml
// originates from a CDATA section (true) or plain text (false).
//
// encoding/xml returns both plain text and CDATA sections as plain
// CharData tokens with no marker, so this scanner mirrors the lexical
// token boundaries (text runs, elements and quoted attributes,
// comments, processing instructions, directives and CDATA sections)
// to pair one record with every CharData token. It does not need to
// decode entities or charsets: CharData tokens and records line up
// structurally, one per text run or CDATA section.
func scanXMLChardataForms(data []byte) []bool {
	forms := make([]bool, 0)
	i := 0
	for i < len(data) {
		if data[i] != '<' {
			start := i
			for i < len(data) && data[i] != '<' {
				i++
			}
			if i > start {
				forms = append(forms, false)
			}
			continue
		}

		if i+1 >= len(data) {
			break
		}
		switch data[i+1] {
		case '?':
			// processing instruction: skip to ?>
			if end := indexAfter(data, i+2, xmlProcInstEnd); end >= 0 {
				i = end + len(xmlProcInstEnd)
			} else {
				return forms
			}
		case '!':
			switch {
			case bytes.HasPrefix(data[i:], xmlCommentStart):
				// comment: skip to -->
				if end := indexAfter(data, i+len(xmlCommentStart), xmlCommentEnd); end >= 0 {
					i = end + len(xmlCommentEnd)
				} else {
					return forms
				}
			case bytes.HasPrefix(data[i:], cdataSectionStart):
				contentStart := i + len(cdataSectionStart)
				end := indexAfter(data, contentStart, cdataSectionEnd)
				if end < 0 {
					// unterminated CDATA section; encoding/xml will
					// report a syntax error when it reaches it
					forms = append(forms, true)
					return forms
				}
				forms = append(forms, true)
				i = end + len(cdataSectionEnd)
			default:
				// directive (e.g. <!DOCTYPE ...>)
				i = skipXMLDirective(data, i+2)
			}
		default:
			// start or end element tag: skip to its closing '>',
			// respecting quoted attribute values
			i = skipXMLTag(data, i+1)
		}
	}
	return forms
}

// skipXMLTag skips over an element start/end tag starting just after
// the leading '<', returning the offset just past its closing '>'.
// Quoted attribute values (which may contain '>' or '<![CDATA[') do not
// terminate the tag.
func skipXMLTag(data []byte, i int) int {
	var inquote byte
	for ; i < len(data); i++ {
		c := data[i]
		if inquote != 0 {
			if c == inquote {
				inquote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inquote = c
		case '>':
			return i + 1
		}
	}
	return i
}

// skipXMLDirective skips over a directive body starting just after the
// leading '<!', returning the offset just past its closing '>'. Nested
// markup angle brackets, quoted strings and comments are handled with
// the same rules encoding/xml applies to directives.
func skipXMLDirective(data []byte, i int) int {
	depth := 0
	var inquote byte
	for i < len(data) {
		c := data[i]
		if inquote != 0 {
			if c == inquote {
				inquote = 0
			}
			i++
			continue
		}
		switch {
		case c == '"' || c == '\'':
			inquote = c
			i++
		case c == '>':
			if depth == 0 {
				return i + 1
			}
			depth--
			i++
		case c == '<' && bytes.HasPrefix(data[i:], xmlCommentStart):
			end := indexAfter(data, i+len(xmlCommentStart), xmlCommentEnd)
			if end < 0 {
				return len(data)
			}
			i = end + len(xmlCommentEnd)
		case c == '<':
			depth++
			i++
		default:
			i++
		}
	}
	return i
}
