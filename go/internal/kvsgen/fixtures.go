package kvsgen

import (
	"fmt"

	"github.com/yacchi/ebml/matroska"
)

// Facts records structural expectations for a fixture, for the manifest
// (fixtures/kvs/README.json) and for test assertions.
type Facts struct {
	Description        string   `json:"description"`
	Fragments          int      `json:"fragments"`
	Segments           int      `json:"segments"`
	Clusters           int      `json:"clusters"`
	SimpleBlocks       int      `json:"simple_blocks"`
	EBMLHeaders        int      `json:"ebml_headers"`
	UnknownSizeSegment bool     `json:"unknown_size_segment"`
	KnownSizeCluster   bool     `json:"known_size_cluster"`
	ContactIDs         []string `json:"contact_ids"`
	ProducerTimestamps []string `json:"producer_timestamps"`
	FragmentNumbers    []string `json:"fragment_numbers"`
	Bytes              int      `json:"bytes"`
	Notes              string   `json:"notes"`
}

// Fixture is one generated synthetic KVS stream plus its metadata.
type Fixture struct {
	Name    string
	Comment string // multi-line layout description for the .hex header
	Data    []byte
	Facts   Facts
}

// BuildAll returns every KVS fixture. Order is stable.
func BuildAll() []Fixture {
	return []Fixture{
		topologyBasic(),
		twoTracks(),
		multiCluster(),
		multiSegment(),
		taglessSingle(),
		taglessConsecutive(),
		partialTags(),
		filterMismatch(),
		gap(),
		falseEBMLMagicInPCM(),
		tailLastFragment(),
		scaledTimestamps(),
		unknownElements(),
		knownSizeCluster(),
		connectRealShape(),
	}
}

// scaledTimestamps is the fixture that makes timestamp scaling observable: its
// Info declares a TimestampScale of 100_000 ns instead of the 1_000_000 default,
// and its single unknown-size Cluster carries a non-zero Timestamp plus a
// SimpleBlock whose relative timecode is NEGATIVE, which is legal and places that
// block before its Cluster's timestamp. A consumer that forgets the scale, or
// that reads the relative timecode as unsigned, cannot reproduce these times.
func scaledTimestamps() Fixture {
	const scale = 100000
	s := newStream()
	s.ebmlHeader()
	s.unknownMaster(matroska.IDSegment, func() {
		s.info(segmentUID(0xA0), scale)
		s.tracks(false)
		s.tags("8000000000.000", "scaled-0", fakeContactA)
		s.unknownCluster(1000,
			block{-20, pcm(24, 0x11)},
			block{0, pcm(24, 0x12)},
			block{20, pcm(24, 0x13)},
		)
	})
	data := s.bytes()
	return Fixture{
		Name: "scaled_timestamps",
		Comment: joinLines(
			"scaled_timestamps: ONE fragment whose Info declares TimestampScale=100000 ns",
			"(NOT the 1000000 default) together with a synthetic SegmentUUID. Its known-size",
			"Cluster has Timestamp=1000 and three SimpleBlocks with relative timecodes",
			"-20, 0 and +20: the first is NEGATIVE, so its absolute time precedes the",
			"Cluster timestamp. Absolute block time is (cluster_timestamp +",
			"relative_timecode) * TimestampScale, both operands being in scale units.",
		),
		Data: data,
		Facts: Facts{
			Description:        "Non-default TimestampScale and a negative relative block timecode.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       3,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"8000000000.000"},
			FragmentNumbers:    []string{"scaled-0"},
			Notes:              "TimestampScale=100000; block timecodes -20/0/+20 around Cluster Timestamp 1000.",
		},
	}
}

// unknownElements is the fixture that proves an element no registry knows costs
// nothing: the Segment carries an unregistered LEAF (0x4FFE, a decodable uint)
// and an unregistered MASTER-shaped element (0x4FFF) holding two ordinary child
// leaves. With the standard registry the cursor reads 0x4FFF as one opaque binary
// leaf whose bytes stay complete; registering it as a master is what makes those
// children nest as elements of their own.
//
// Both IDs come from internal/ebmltest and are checked against the published
// schema, because this fixture's whole premise is that no registry can know
// them: the leaf was 0xEE until the schema check showed that 0xEE is Matroska's
// BlockAddID.
func unknownElements() Fixture {
	s := newStream()
	s.ebmlHeader()
	s.unknownMaster(matroska.IDSegment, func() {
		s.info(segmentUID(0xB0), defaultTimestampScale)
		s.uint(idUnregisteredLeaf, 42)
		// The writer knows no element either, so calling it a master is the
		// caller's decision — exactly the shape a consumer's registry would have
		// to teach the reader about.
		s.master(idUnregisteredMaster, func() {
			s.str(matroska.IDName, "vendor-box")
			s.uint(matroska.IDTrackNumber, 7)
		})
		s.tracks(false)
		s.tags("9000000000.000", "unknown-0", fakeContactA)
		s.unknownCluster(0, block{0, pcm(24, 0x21)})
	})
	data := s.bytes()
	return Fixture{
		Name: "unknown_elements",
		Comment: joinLines(
			"unknown_elements: ONE fragment whose Segment carries two elements no registry",
			"knows: the LEAF 0x4FFE (payload 0x2A = 42, a decodable uint) and the",
			"MASTER-SHAPED 0x4FFF holding Name=\"vendor-box\" and TrackNumber=7.",
			"With the standard RFC 9559 registry both classify as binary leaves: the reader",
			"honours their declared sizes, keeps their bytes complete, and reads every",
			"element after them normally. Registering 0x4FFF as a master (or re-parsing its",
			"payload) recovers the two children as elements.",
		),
		Data: data,
		Facts: Facts{
			Description:        "Segment carrying an unregistered leaf and an unregistered master-shaped element.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       1,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"9000000000.000"},
			FragmentNumbers:    []string{"unknown-0"},
			Notes:              "0x4FFE leaf and 0x4FFF master-shaped element are unregistered; the reader never breaks on them.",
		},
	}
}

func multiCluster() Fixture {
	s := newStream()
	s.ebmlHeader()
	s.unknownMaster(matroska.IDSegment, func() {
		s.info(segmentUID(0x20), defaultTimestampScale)
		s.tracks(false)
		s.tags("1500000000.000", "multi-cluster", fakeContactA)
		s.unknownCluster(0,
			block{0, pcm(24, 0x61)},
			block{10, pcm(24, 0x62)},
		)
		s.unknownCluster(1024,
			block{0, pcm(24, 0x71)},
			block{10, pcm(24, 0x72)},
		)
	})
	data := s.bytes()
	return Fixture{
		Name: "multi_cluster",
		Comment: joinLines(
			"multi_cluster: ONE EBML header + ONE unknown-size Segment containing",
			"Info, Tracks, Tags, and TWO unknown-size Clusters. Each Cluster has a Timestamp",
			"and two SimpleBlocks. Both Cluster end_master events occur before the",
			"Segment is closed by FinalizeEOF.",
		),
		Data: data,
		Facts: Facts{
			Description:        "One unknown-size Segment containing two unknown-size Clusters.",
			Fragments:          1,
			Segments:           1,
			Clusters:           2,
			SimpleBlocks:       4,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"1500000000.000"},
			FragmentNumbers:    []string{"multi-cluster"},
			Notes:              "Both Clusters close by the deny-only child rule before the Segment closes at EOF.",
		},
	}
}

func topologyBasic() Fixture {
	return topologyBasicWithStrategy("topology_basic", unknownClusterSize, "topology_basic: ONE EBML header + ONE unknown-size Segment.",
		"Segment { Info, Tracks, Tags(ContactId=A), Cluster(unknown-size, 3 SimpleBlocks) }.",
		"Property: the Cluster closes at the first registered non-child element or EOF, while the Segment stays open.",
		"This is the real KVS topology and exercises RFC 9559 deny-only Cluster closure.")
}

func knownSizeCluster() Fixture {
	return topologyBasicWithStrategy("known_size_cluster", knownClusterSize,
		"known_size_cluster: the legal Matroska but not KVS shape retained for coverage.",
		"Segment { Info, Tracks, Tags(ContactId=A), Cluster(known-size, 3 SimpleBlocks) }.",
		"KVS sends unknown-size Clusters; this fixture preserves the old declared-end path.")
}

func topologyBasicWithStrategy(name string, strategy clusterSizeStrategy, comment ...string) Fixture {
	s := newStream()
	s.fragment(fragOpts{
		producerTS:     "1000000000.000",
		fragmentNumber: "91343852333181000000000000000000000000000000001",
		contactID:      fakeContactA,
		includeInfo:    true,
		audioParams:    true,
		uidSeed:        0x10,
		clusterTS:      0,
		blocks: []block{
			{0, pcm(32, 0x01)},
			{10, pcm(32, 0x02)},
			{20, pcm(32, 0x03)},
		},
		clusterSize: strategy,
	})
	data := s.bytes()
	return Fixture{
		Name:    name,
		Comment: joinLines(comment...),
		Data:    data,
		Facts: Facts{
			Description:        "Single fragment: unknown-size Segment holding a Cluster.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       3,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   strategy == knownClusterSize,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"1000000000.000"},
			FragmentNumbers:    []string{"91343852333181000000000000000000000000000000001"},
			Notes:              "Cluster and Segment closure follow their declared size strategies.",
		},
	}
}

func twoTracks() Fixture {
	s := newStream()
	s.ebmlHeader()
	s.unknownMaster(matroska.IDSegment, func() {
		s.info(segmentUID(0x18), defaultTimestampScale)
		s.tracks(true)
		s.tags("1100000000.000", "two-track-0", fakeContactA)
		s.unknownTwoTrackCluster(0,
			struct {
				track uint64
				block
			}{1, block{0, pcm(24, 0x11)}},
			struct {
				track uint64
				block
			}{2, block{0, pcm(32, 0x21)}},
			struct {
				track uint64
				block
			}{1, block{10, pcm(16, 0x31)}},
		)
	})
	data := s.bytes()
	return Fixture{
		Name: "two_tracks",
		Comment: joinLines(
			"two_tracks: ONE fragment with both named audio tracks carrying blocks in the",
			"SAME unknown-size Cluster. Track 1 has 40 PCM bytes total and Track 2 has 32",
			"PCM bytes in one block; the unequal lengths exercise per-track selection.",
		),
		Data: data,
		Facts: Facts{
			Description:        "One Cluster carries blocks for both named audio tracks.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       3,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"1100000000.000"},
			FragmentNumbers:    []string{"two-track-0"},
			Notes:              "Track 1 uses two blocks of 24 and 16 bytes; Track 2 uses one block of 32 bytes.",
		},
	}
}

func multiSegment() Fixture {
	tss := []string{"1000000000.000", "1000000001.024", "1000000002.048", "1000000003.072"}
	fns := []string{"...001", "...002", "...003", "...004"}
	s := newStream()
	for i := 0; i < 4; i++ {
		s.fragment(fragOpts{
			producerTS:     tss[i],
			fragmentNumber: fmt.Sprintf("9134385233318100000000000000000000000000000000%d", i+1),
			contactID:      fakeContactA,
			includeInfo:    i == 0,
			uidSeed:        0x30,
			clusterTS:      uint64(i) * 1024,
			blocks: []block{
				{0, pcm(32, byte(0x10+i))},
				{10, pcm(32, byte(0x20+i))},
			},
		})
	}
	data := s.bytes()
	return Fixture{
		Name: "multi_segment",
		Comment: joinLines(
			"multi_segment: 4 concatenated fragments (EBML+unknown-size Segment each),",
			"increasing ProducerTimestamp and FragmentNumber - a continuous GetMedia stream.",
			"Each Segment is closed structurally when the next fragment's EBML header is",
			"peeked (top-level boundary), NOT by scanning bytes for the EBML magic.",
		),
		Data: data,
		Facts: Facts{
			Description:        "4-fragment continuous stream; per-fragment unknown-size Segments.",
			Fragments:          4,
			Segments:           4,
			Clusters:           4,
			SimpleBlocks:       8,
			EBMLHeaders:        4,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: tss,
			FragmentNumbers:    fns,
			Notes:              "Segment boundaries detected via top-level EBML sibling, not magic scan.",
		},
	}
}

func taglessSingle() Fixture {
	s := newStream()
	// fragment 0: normal; fragment 1: NO Tags element at all; fragment 2: normal.
	s.fragment(fragOpts{
		producerTS: "2000000000.000", fragmentNumber: "tag-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x40, clusterTS: 0,
		blocks: []block{{0, pcm(24, 0x01)}},
	})
	s.fragment(fragOpts{
		producerTS: "2000000001.024", fragmentNumber: "tag-1", omitTags: true,
		clusterTS: 1024, blocks: []block{{0, pcm(24, 0x02)}},
	})
	s.fragment(fragOpts{
		producerTS: "2000000002.048", fragmentNumber: "tag-2", contactID: fakeContactA,
		clusterTS: 2048, blocks: []block{{0, pcm(24, 0x03)}},
	})
	data := s.bytes()
	return Fixture{
		Name: "tagless_single",
		Comment: joinLines(
			"tagless_single: 3 fragments where the MIDDLE fragment's Segment omits the",
			"entire Tags element (no ContactId). Structural parsing is unaffected: the",
			"Segment still opens/closes on structure. Contact attribution is a policy",
			"decision left to the reader, not the cursor.",
		),
		Data: data,
		Facts: Facts{
			Description:        "3 fragments; middle fragment has no Tags element.",
			Fragments:          3,
			Segments:           3,
			Clusters:           3,
			SimpleBlocks:       3,
			EBMLHeaders:        3,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA, "", fakeContactA},
			ProducerTimestamps: []string{"2000000000.000", "2000000001.024", "2000000002.048"},
			FragmentNumbers:    []string{"tag-0", "tag-1", "tag-2"},
			Notes:              "Middle fragment tagless; cursor still parses cleanly.",
		},
	}
}

func taglessConsecutive() Fixture {
	s := newStream()
	// fragment 0: normal; fragments 1 and 2: tagless; fragment 3: normal.
	s.fragment(fragOpts{
		producerTS: "3000000000.000", fragmentNumber: "c-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x50, clusterTS: 0, blocks: []block{{0, pcm(24, 0x01)}},
	})
	s.fragment(fragOpts{
		producerTS: "3000000001.024", fragmentNumber: "c-1", omitTags: true,
		includeInfo: true, uidSeed: 0x50,
		clusterTS: 1024, blocks: []block{{0, pcm(24, 0x02)}},
	})
	s.fragment(fragOpts{
		producerTS: "3000000002.048", fragmentNumber: "c-2", omitTags: true,
		includeInfo: true, uidSeed: 0x50,
		clusterTS: 2048, blocks: []block{{0, pcm(24, 0x03)}},
	})
	s.fragment(fragOpts{
		producerTS: "3000000003.072", fragmentNumber: "c-3", contactID: fakeContactA,
		clusterTS: 3072, blocks: []block{{0, pcm(24, 0x04)}},
	})
	data := s.bytes()
	return Fixture{
		Name: "tagless_consecutive",
		Comment: joinLines(
			"tagless_consecutive: 4 fragments where the two MIDDLE fragments both omit",
			"Tags. Verifies the cursor is unaffected by consecutive tagless fragments.",
		),
		Data: data,
		Facts: Facts{
			Description:        "4 fragments; two consecutive middle fragments are tagless.",
			Fragments:          4,
			Segments:           4,
			Clusters:           4,
			SimpleBlocks:       4,
			EBMLHeaders:        4,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA, "", "", fakeContactA},
			ProducerTimestamps: []string{"3000000000.000", "3000000001.024", "3000000002.048", "3000000003.072"},
			FragmentNumbers:    []string{"c-0", "c-1", "c-2", "c-3"},
			Notes:              "Two consecutive tagless fragments; cursor parses cleanly.",
		},
	}

}

func partialTags() Fixture {
	s := newStream()
	s.fragment(fragOpts{
		producerTS: "3200000000.000", fragmentNumber: "partial-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x58, clusterTS: 0,
		blocks: []block{{0, pcm(24, 0x11)}},
	})
	s.fragment(fragOpts{
		producerTS: "3200000001.024", fragmentNumber: "partial-1",
		includeInfo: true, uidSeed: 0x58, omitIdentity: true, clusterTS: 1024,
		blocks: []block{{0, pcm(24, 0x12)}},
	})
	data := s.bytes()
	return Fixture{
		Name: "partial_tags",
		Comment: joinLines(
			"partial_tags: TWO fragments with the same SegmentUUID. The second fragment",
			"has a PRESENT, populated Tags element carrying producer timestamp, fragment",
			"number, and continuation token, but omits ContactId and InstanceId.",
			"Per-key identity inheritance is a consumer policy, not assembler behavior.",
		),
		Data: data,
		Facts: Facts{
			Description:        "Second fragment has populated partial Tags without identity keys.",
			Fragments:          2,
			Segments:           2,
			Clusters:           2,
			SimpleBlocks:       2,
			EBMLHeaders:        2,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA, ""},
			ProducerTimestamps: []string{"3200000000.000", "3200000001.024"},
			FragmentNumbers:    []string{"partial-0", "partial-1"},
			Notes:              "The second Segment repeats the UUID but omits ContactId and InstanceId only.",
		},
	}
}

func filterMismatch() Fixture {
	// fragments 0,1 = ContactA; fragment 2,3 switch to ContactB (transfer-style).
	ids := []string{fakeContactA, fakeContactA, fakeContactB, fakeContactB}
	tss := []string{"4000000000.000", "4000000001.024", "4000000002.048", "4000000003.072"}
	s := newStream()
	for i := 0; i < 4; i++ {
		s.fragment(fragOpts{
			producerTS: tss[i], fragmentNumber: fmt.Sprintf("fm-%d", i), contactID: ids[i],
			includeInfo: i == 0, uidSeed: 0x60, clusterTS: uint64(i) * 1024,
			blocks: []block{{0, pcm(24, byte(0x30+i))}},
		})
	}
	data := s.bytes()
	return Fixture{
		Name: "filter_mismatch",
		Comment: joinLines(
			"filter_mismatch: 4 fragments where ContactId changes from A to B at",
			"fragment index 2 (a transfer-style boundary). The cursor parses all four",
			"identically; the A->B contact boundary is a reader-side source_filter",
			"decision, not a structural event.",
		),
		Data: data,
		Facts: Facts{
			Description:        "ContactId switches A->B at fragment 2.",
			Fragments:          4,
			Segments:           4,
			Clusters:           4,
			SimpleBlocks:       4,
			EBMLHeaders:        4,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         ids,
			ProducerTimestamps: tss,
			FragmentNumbers:    []string{"fm-0", "fm-1", "fm-2", "fm-3"},
			Notes:              "Contact boundary is reader policy; structure is uniform.",
		},
	}
}

func gap() Fixture {
	// FragmentNumbers/timecodes jump: 0,1, (missing 2), 4 — a dropped fragment.
	specs := []struct {
		fn string
		ts string
		tc uint64
	}{
		{"gap-0", "5000000000.000", 0},
		{"gap-1", "5000000001.024", 1024},
		{"gap-4", "5000000004.096", 4096}, // fragments 2,3 dropped
	}
	s := newStream()
	for i, spec := range specs {
		s.fragment(fragOpts{
			producerTS: spec.ts, fragmentNumber: spec.fn, contactID: fakeContactA,
			includeInfo: i == 0, uidSeed: 0x70, clusterTS: spec.tc,
			blocks: []block{{0, pcm(24, byte(0x50+i))}},
		})
	}
	data := s.bytes()
	return Fixture{
		Name: "gap",
		Comment: joinLines(
			"gap: 3 fragments with a non-contiguous jump in FragmentNumber/Timestamp",
			"(fragments 2 and 3 dropped). The cursor emits the same clean structural",
			"log; gap detection (silence fill) is a reader concern over timestamps.",
		),
		Data: data,
		Facts: Facts{
			Description:        "Non-contiguous fragments (dropped 2 and 3).",
			Fragments:          3,
			Segments:           3,
			Clusters:           3,
			SimpleBlocks:       3,
			EBMLHeaders:        3,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"5000000000.000", "5000000001.024", "5000000004.096"},
			FragmentNumbers:    []string{"gap-0", "gap-1", "gap-4"},
			Notes:              "Discontinuity visible in tags/timecodes, not in structure.",
		},
	}
}

func falseEBMLMagicInPCM() Fixture {
	// One fragment; a SimpleBlock whose PCM payload embeds 0x1A 0x45 0xDF 0xA3.
	magicPCM := pcmWithEBMLMagic(48)
	s := newStream()
	s.fragment(fragOpts{
		producerTS: "6000000000.000", fragmentNumber: "magic-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x80, clusterTS: 0,
		blocks: []block{
			{0, pcm(24, 0x01)},
			{10, magicPCM}, // contains the EBML magic bytes
			{20, pcm(24, 0x03)},
		},
	})
	data := s.bytes()
	return Fixture{
		Name: "false_ebml_magic_in_pcm",
		Comment: joinLines(
			"false_ebml_magic_in_pcm: a SimpleBlock whose synthetic PCM CONTAINS the 4",
			"EBML magic bytes 1A 45 DF A3. A byte-scanning splitter (the driver's current",
			"isEBMLHeaderAt heuristic) would false-trigger a fragment split inside the PCM.",
			"The size-driven cursor reads the SimpleBlock as ONE leaf of its declared size",
			"and never scans for the magic, so there is exactly one EBML header (the real",
			"fragment header) and no spurious Segment/EBML split inside the audio.",
		),
		Data: data,
		Facts: Facts{
			Description:        "SimpleBlock PCM embeds the EBML magic; must not mis-split.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       3,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"6000000000.000"},
			FragmentNumbers:    []string{"magic-0"},
			Notes:              "Proves size-driven parsing beats the byte-scan heuristic.",
		},
	}
}

func tailLastFragment() Fixture {
	// Two fragments; the LAST has no following Segment. Its Cluster completes and
	// is observable via end_master before any EOF; the Segment closes at EOF only.
	s := newStream()
	s.fragment(fragOpts{
		producerTS: "7000000000.000", fragmentNumber: "tail-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x90, clusterTS: 0, blocks: []block{{0, pcm(24, 0x01)}},
	})
	s.fragment(fragOpts{
		producerTS: "7000000001.024", fragmentNumber: "tail-1", contactID: fakeContactA,
		clusterTS: 1024, blocks: []block{
			{0, pcm(24, 0x02)},
			{10, pcm(24, 0x03)},
		},
	})
	data := s.bytes()
	return Fixture{
		Name: "tail_last_fragment",
		Comment: joinLines(
			"tail_last_fragment: 2 fragments; the final Segment has NO following Segment.",
			"Its unknown-size Cluster completes at EOF in this terminal single-document stream;",
			"the Segment is also closed only by FinalizeEOF. The fixture covers the final",
			"fragment path without a following Segment (no fabricated next document).",
		),
		Data: data,
		Facts: Facts{
			Description:        "Final fragment's Cluster observable before EOF; Segment closes at EOF.",
			Fragments:          2,
			Segments:           2,
			Clusters:           2,
			SimpleBlocks:       3,
			EBMLHeaders:        2,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"7000000000.000", "7000000001.024"},
			FragmentNumbers:    []string{"tail-0", "tail-1"},
			Notes:              "Last Cluster emits via end_master; tail latency removed.",
		},
	}
}

func joinLines(lines ...string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

func connectRealShape() Fixture {
	s := newStream()
	s.ebmlHeader()
	s.unknownMaster(matroska.IDSegment, func() {
		s.info(segmentUID(0xC0), defaultTimestampScale)
		s.tracks(true)
		s.tagsWithPairs(
			tagPair{"ContactId", fakeContactA},
			tagPair{"InstanceId", fakeInstance},
			tagPair{"MimeType", "audio/L16;rate=8000;channels=1"},
			tagPair{"AUDIO_TO_CUSTOMER", "AUDIO_TO_CUSTOMER"},
			tagPair{"AUDIO_FROM_CUSTOMER", "AUDIO_FROM_CUSTOMER"},
		)
		s.tagsWithPairs(
			tagPair{"AWS_KINESISVIDEO_FRAGMENT_NUMBER", "connect-real-0"},
			tagPair{"AWS_KINESISVIDEO_SERVER_TIMESTAMP", "1000000000.000"},
			tagPair{"AWS_KINESISVIDEO_PRODUCER_TIMESTAMP", "1000000000.000"},
		)
		s.unknownTwoTrackCluster(0,
			struct {
				track uint64
				block
			}{1, block{0, pcm(24, 0x41)}},
			struct {
				track uint64
				block
			}{2, block{0, pcm(24, 0x51)}},
			struct {
				track uint64
				block
			}{1, block{10, pcm(24, 0x61)}},
			struct {
				track uint64
				block
			}{2, block{10, pcm(24, 0x71)}},
		)
		s.tagsWithPairs(
			tagPair{"ContactId", fakeContactA},
			tagPair{"InstanceId", fakeInstance},
			tagPair{"MimeType", "audio/L16;rate=8000;channels=1"},
			tagPair{"AUDIO_TO_CUSTOMER", "AUDIO_TO_CUSTOMER"},
			tagPair{"AUDIO_FROM_CUSTOMER", "AUDIO_FROM_CUSTOMER"},
		)
		s.tagsWithPairs(
			tagPair{"AWS_KINESISVIDEO_MILLIS_BEHIND_NOW", "0"},
			tagPair{"AWS_KINESISVIDEO_CONTINUATION_TOKEN", fakeContinuation},
		)
	})
	data := s.bytes()
	return Fixture{
		Name: "connect_real_shape",
		Comment: joinLines(
			"connect_real_shape: the real Amazon Connect layout with an unknown-size Segment",
			"and unknown-size Cluster. Two separate Tags elements appear before the Cluster:",
			"identity/audio tags, then fragment number and server/producer timestamps.",
			"The Cluster carries four SimpleBlocks, two per named track. Two more separate",
			"Tags elements appear after the Cluster; MILLIS_BEHIND_NOW and CONTINUATION_TOKEN",
			"occur ONLY after it. All values are fabricated synthetic data.",
		),
		Data: data,
		Facts: Facts{
			Description:        "Real Connect-shaped one-fragment layout with four blocks and four Segment Tags elements.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       4,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"1000000000.000"},
			FragmentNumbers:    []string{"connect-real-0"},
			Notes:              "Segment-level Tags appear both before and after the Cluster; AWS_KINESISVIDEO_MILLIS_BEHIND_NOW and AWS_KINESISVIDEO_CONTINUATION_TOKEN occur ONLY after it.",
		},
	}
}
