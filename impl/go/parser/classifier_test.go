package parser

// testKindClassifier classifies the Matroska element IDs used by the parser's
// internal tests (tiny golden + fuzz + defects). The standard classifier lives in package
// matroska, but package parser's internal tests cannot import it (matroska
// imports parser, which would be an import cycle), so this test-only helper
// mirrors the relevant subset using literal IDs. The package under test contains
// no element ID at all -- a classifier is a required constructor argument and
// there is no built-in default to fall back on -- so this helper is complete on
// its own: every ID it does not list is a binary leaf.
func testKindClassifier(id ElementID) Kind {
	switch id {
	case 0x1A45DFA3, // EBML header
		0x18538067, // Segment
		0x114D9B74, // SeekHead
		0x1549A966, // Info
		0x1654AE6B, // Tracks
		0xAE,       // TrackEntry
		0xE1,       // Audio
		0x1F43B675, // Cluster
		0x1254C367, // Tags
		0x7373,     // Tag
		0x63C0,     // Targets
		0x67C8:     // SimpleTag
		return KindMaster
	case 0x4286, // EBMLVersion
		0x42F7,   // EBMLReadVersion
		0x2AD7B1, // TimestampScale
		0xD7,     // TrackNumber
		0x83,     // TrackType
		0xE7:     // Timestamp
		return KindUint
	case 0x9F, // Channels
		0x6264: // BitDepth
		return KindUint

	default:
		return KindBinary
	}
}
