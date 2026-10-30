package yqlib

type XmlPreferences struct {
	Indent          int
	AttributePrefix string
	ContentName     string
	StrictMode      bool
	KeepNamespace   bool
	UseRawToken     bool
	ProcInstPrefix  string
	DirectiveName   string
	SkipProcInst    bool
	SkipDirectives  bool
	// KeepCData preserves the original form and order of XML character
	// data segments (plain text vs CDATA sections) through an XML
	// decode/encode round trip. Defaults to false.
	KeepCData bool
}

func NewDefaultXmlPreferences() XmlPreferences {
	return XmlPreferences{
		Indent:          2,
		AttributePrefix: "+@",
		ContentName:     "+content",
		StrictMode:      false,
		KeepNamespace:   true,
		UseRawToken:     true,
		ProcInstPrefix:  "+p_",
		DirectiveName:   "+directive",
		SkipProcInst:    false,
		SkipDirectives:  false,
		KeepCData:       false,
	}
}

func (p *XmlPreferences) Copy() XmlPreferences {
	return XmlPreferences{
		Indent:          p.Indent,
		AttributePrefix: p.AttributePrefix,
		ContentName:     p.ContentName,
		StrictMode:      p.StrictMode,
		KeepNamespace:   p.KeepNamespace,
		UseRawToken:     p.UseRawToken,
		ProcInstPrefix:  p.ProcInstPrefix,
		DirectiveName:   p.DirectiveName,
		SkipProcInst:    p.SkipProcInst,
		SkipDirectives:  p.SkipDirectives,
		KeepCData:       p.KeepCData,
	}
}

var ConfiguredXMLPreferences = NewDefaultXmlPreferences()
