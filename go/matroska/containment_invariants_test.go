package matroska

import (
	"sort"
	"testing"

	"github.com/yacchi/ebml/parser"
)

// TestSegmentAndClusterChildListsMatchRFC9559 independently re-verifies the two
// seeded complete child lists against the exact sets specified for the F4 fix,
// catching any extra or missing entry and any constant drift.
func TestSegmentAndClusterChildListsMatchRFC9559(t *testing.T) {
	wantSegment := map[parser.ElementID]bool{
		0x114D9B74: true, // SeekHead
		0x1549A966: true, // Info
		0x1F43B675: true, // Cluster
		0x1654AE6B: true, // Tracks
		0x1C53BB6B: true, // Cues
		0x1941A469: true, // Attachments
		0x1043A770: true, // Chapters
		0x1254C367: true, // Tags
	}
	got, ok := LegalChildren(IDSegment)
	if !ok {
		t.Fatal("Segment must have a complete child list")
	}
	if len(got) != len(wantSegment) {
		t.Fatalf("Segment children = %d entries, want %d", len(got), len(wantSegment))
	}
	for _, id := range got {
		if !wantSegment[id] {
			t.Fatalf("Segment children has unexpected entry %s", id)
		}
		delete(wantSegment, id)
	}
	if len(wantSegment) != 0 {
		t.Fatalf("Segment children is missing entries: %v", wantSegment)
	}

	wantCluster := map[parser.ElementID]bool{
		0xE7: true, // Timestamp
		0xA7: true, // Position
		0xAB: true, // PrevSize
		0xA3: true, // SimpleBlock
		0xA0: true, // BlockGroup
	}
	gotC, ok := LegalChildren(IDCluster)
	if !ok {
		t.Fatal("Cluster must have a complete child list")
	}
	if len(gotC) != len(wantCluster) {
		t.Fatalf("Cluster children = %d entries, want %d", len(gotC), len(wantCluster))
	}
	for _, id := range gotC {
		if !wantCluster[id] {
			t.Fatalf("Cluster children has unexpected entry %s", id)
		}
		delete(wantCluster, id)
	}
	if len(wantCluster) != 0 {
		t.Fatalf("Cluster children is missing entries: %v", wantCluster)
	}

	if IDCRC32 != 0xBF {
		t.Fatalf("IDCRC32 = %s, want 0xBF", IDCRC32)
	}
	if IDVoid != 0xEC {
		t.Fatalf("IDVoid = %s, want 0xEC", IDVoid)
	}
	if _, ok := globalElements[0xBF]; !ok {
		t.Fatal("CRC-32 (0xBF) must be a global element")
	}
	if _, ok := globalElements[0xEC]; !ok {
		t.Fatal("Void (0xEC) must be a global element")
	}
	if len(globalElements) != 2 {
		t.Fatalf("globalElements has %d entries, want exactly 2", len(globalElements))
	}
}

// TestDeprecatedClusterChildrenStayUnregistered pins the pairing that makes the
// Cluster child list safe to call COMPLETE while omitting two elements the
// schema declares. SilentTracks and EncryptedBlock are legal Cluster children,
// so registering either without adding it to completeChildren would make it end
// every unknown-size Cluster it appears in -- silently truncating a fragment on
// a legal file. They are the only two elements in the whole schema this
// registry deliberately does not name, and internal/specconform reports the
// same pairing against the published schema; this test states it without
// needing the schema on disk.
func TestDeprecatedClusterChildrenStayUnregistered(t *testing.T) {
	deprecated := []struct {
		id   parser.ElementID
		name string
	}{
		{0x5854, "SilentTracks"},
		{0x58D7, "SilentTrackNumber"},
		{0xAF, "EncryptedBlock"},
	}
	for _, element := range deprecated {
		if info, ok := Lookup(element.id); ok {
			t.Fatalf("%s (%s) is registered as %q; add it to completeChildren[IDCluster] "+
				"in the same change or it will end every unknown-size Cluster it appears in",
				element.name, element.id, info.Name)
		}
		if _, ok := IDForName(element.name); ok {
			t.Fatalf("%s resolves by name; see the comment on completeChildren", element.name)
		}
		if EndsUnknownSizeMaster(IDCluster, element.id) {
			t.Fatalf("%s must never end an unknown-size Cluster", element.name)
		}
	}
}

// TestLegalChildrenIsSortedByID pins the documented ordering guarantee.
func TestLegalChildrenIsSortedByID(t *testing.T) {
	for _, master := range []parser.ElementID{IDSegment, IDCluster} {
		children, ok := LegalChildren(master)
		if !ok {
			t.Fatalf("LegalChildren(%s) should have a complete list", master)
		}
		if !sort.SliceIsSorted(children, func(i, j int) bool { return children[i] < children[j] }) {
			t.Fatalf("LegalChildren(%s) = %v is not sorted", master, children)
		}
	}
}

// TestLegalChildrenSurvivesAppendMutation goes further than the worker's copy
// check: it appends past the returned slice's length (and possibly into its
// capacity) and mutates in place, then re-queries to prove the registry's own
// backing array was never touched, even if the returned slice had spare
// capacity that an append could silently reuse.
func TestLegalChildrenSurvivesAppendMutation(t *testing.T) {
	first, ok := LegalChildren(IDCluster)
	if !ok {
		t.Fatal("Cluster should have a complete child list")
	}
	// Mutate every element and try to grow past len via append, which would
	// corrupt a shared backing array if cap(first) > len(first).
	for i := range first {
		first[i] = 0xDEADBEEF
	}
	_ = append(first, 0xDEADBEEF)

	second, ok := LegalChildren(IDCluster)
	if !ok {
		t.Fatal("Cluster should still have a complete child list")
	}
	for _, id := range second {
		if id == 0xDEADBEEF {
			t.Fatal("LegalChildren leaked a mutation through a shared backing array")
		}
	}
	want := []parser.ElementID{IDTimestamp, IDPosition, IDPrevSize, IDSimpleBlock, IDBlockGroup}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(second) != len(want) {
		t.Fatalf("second call returned %d entries, want %d (unaffected by first call's mutation)", len(second), len(want))
	}
}

// TestLegalChildrenFalseForMasterWithNoList checks every master NOT seeded,
// including ones that ARE registered masters, to make sure only Segment and
// Cluster claim completeness.
func TestLegalChildrenFalseForMasterWithNoList(t *testing.T) {
	for _, master := range []parser.ElementID{
		IDInfo, IDTracks, IDTags, IDBlockGroup, IDTrackEntry, IDCues, IDChapters,
		IDAttachments, IDSeekHead, IDEBML, parser.ElementID(0xEEEE),
	} {
		if _, ok := LegalChildren(master); ok {
			t.Fatalf("LegalChildren(%s) claimed a complete list, want false", master)
		}
	}
}

// TestEndsUnknownSizeMasterFalseWheneverUncertain targets the exact "false
// whenever uncertain" contract, not merely a happy-path true/false pair.
func TestEndsUnknownSizeMasterFalseWheneverUncertain(t *testing.T) {
	cases := []struct {
		name       string
		open, next parser.ElementID
	}{
		{"open has no complete list: Info", IDInfo, IDTags},
		{"open has no complete list: Tracks", IDTracks, IDTags},
		{"open has no complete list: Tags", IDTags, IDCluster},
		{"open has no complete list: BlockGroup", IDBlockGroup, IDCluster},
		{"open has no complete list: unregistered", parser.ElementID(0xEEEE), IDTags},
		{"open == 0", parser.ElementID(0), IDTags},
		{"unregistered next under known open (Segment)", IDSegment, parser.ElementID(0xEEEE)},
		{"unregistered next under known open (Cluster)", IDCluster, parser.ElementID(0xEEEE)},
		{"global next under Segment (Void)", IDSegment, IDVoid},
		{"global next under Segment (CRC-32)", IDSegment, IDCRC32},
		{"global next under Cluster (Void)", IDCluster, IDVoid},
		{"global next under Cluster (CRC-32)", IDCluster, IDCRC32},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if EndsUnknownSizeMaster(c.open, c.next) {
				t.Fatalf("EndsUnknownSizeMaster(%s, %s) = true, want false (uncertain case must stay false)", c.open, c.next)
			}
		})
	}
}

// TestEndsUnknownSizeMasterTrueOnlyForRegisteredNonGlobalAbsentFromList checks
// the positive direction is exactly as narrow as documented: true only for a
// registered, non-global element absent from a COMPLETE list.
func TestEndsUnknownSizeMasterTrueOnlyForRegisteredNonGlobalAbsentFromList(t *testing.T) {
	// Every Segment-level master ends a Cluster (none of them are Cluster
	// children), independently re-derived from the RFC list rather than reusing
	// the worker's own single "Tags ends Cluster" case.
	for _, next := range []parser.ElementID{IDSeekHead, IDInfo, IDTracks, IDCues, IDAttachments, IDChapters, IDTags} {
		if !EndsUnknownSizeMaster(IDCluster, next) {
			t.Errorf("EndsUnknownSizeMaster(Cluster, %s) = false, want true", next)
		}
	}
	// Every Cluster child must NOT end a Cluster.
	for _, next := range []parser.ElementID{IDTimestamp, IDPosition, IDPrevSize, IDSimpleBlock, IDBlockGroup} {
		if EndsUnknownSizeMaster(IDCluster, next) {
			t.Errorf("EndsUnknownSizeMaster(Cluster, %s) = true, want false (legal child)", next)
		}
	}
	// Every Segment child must NOT end a Segment.
	for _, next := range []parser.ElementID{IDSeekHead, IDInfo, IDCluster, IDTracks, IDCues, IDAttachments, IDChapters, IDTags} {
		if EndsUnknownSizeMaster(IDSegment, next) {
			t.Errorf("EndsUnknownSizeMaster(Segment, %s) = true, want false (legal child)", next)
		}
	}
}
