package parser

// Common constants used together with the parser.
const (
	UnknownSize int64 = -1

	MaxElementIDLength   = 4
	MaxElementSizeLength = 8
)

// WebM/Matroska element IDs (partial; extend as needed).
const (
	ElementIDEBML        uint32 = 0x1A45DFA3
	ElementIDSegment     uint32 = 0x18538067
	ElementIDEBMLVersion uint32 = 0x4286
	ElementIDEBMLReadVer uint32 = 0x42F7
	ElementIDDocType     uint32 = 0x4282
	ElementIDCRC32       uint32 = 0xBF
	ElementIDVoid        uint32 = 0xEC
)

func NameForElementID(id uint32) string {
	switch id {
	case ElementIDEBML:
		return "EBML"
	case ElementIDSegment:
		return "Segment"
	case ElementIDEBMLVersion:
		return "EBMLVersion"
	case ElementIDEBMLReadVer:
		return "EBMLReadVersion"
	case ElementIDDocType:
		return "DocType"
	case ElementIDCRC32:
		return "CRC-32"
	case ElementIDVoid:
		return "Void"
	default:
		return ""
	}
}

func KindForElementID(id uint32) Kind {
	switch id {
	case ElementIDEBML, ElementIDSegment:
		return KindMaster
	case ElementIDEBMLVersion, ElementIDEBMLReadVer:
		return KindUint
	case ElementIDDocType, ElementIDCRC32, ElementIDVoid:
		return KindBinary
	default:
		return KindBinary
	}
}
