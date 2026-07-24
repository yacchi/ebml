package parser

// testKindClassifier classifies the Matroska element IDs used by the parser's
// internal tests (tiny golden + fuzz + defects). The standard classifier lives in package
// matroska, but package parser's internal tests cannot import it (matroska
// imports parser, which would be an import cycle), so this test-only helper
// mirrors the relevant subset using literal IDs. Unknown IDs delegate to the
// parser's own default classifier.
func testKindClassifier(id ElementID) Kind {
	switch id {
	case 0x114D9B74, // SeekHead
		0x1549A966, // Info
		0x1654AE6B, // Tracks
		0xAE,       // TrackEntry
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
	default:
		return defaultKindClassifier(id)
	}
}
