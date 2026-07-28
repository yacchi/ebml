package kvsgen

import (
	"fmt"

	"github.com/yacchi/ebml/impl/go/matroska"
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
		trackOrderSwapped(),
		shortBlockMidTrack(),
		taglessTail(),
		streamReuse(),
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
			trackBlock{1, block{0, pcm(24, 0x11)}},
			trackBlock{2, block{0, pcm(32, 0x21)}},
			trackBlock{1, block{10, pcm(16, 0x31)}},
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

// connectEpochMillis is connect_real_shape's Cluster Timestamp: the same instant its
// AWS_KINESISVIDEO_PRODUCER_TIMESTAMP names (1000000000 seconds since the epoch),
// counted in the milliseconds that defaultTimestampScale makes one tick.
const connectEpochMillis = 1_000_000_000 * 1_000

// epochTag renders epoch milliseconds as the decimal-seconds string a KVS
// producer/server timestamp tag carries, so a fixture's tags and its Cluster
// Timestamp cannot drift apart.
func epochTag(millis uint64) string {
	return fmt.Sprintf("%d.%03d", millis/1000, millis%1000)
}

func connectRealShape() Fixture {
	s := newStream()
	// The Cluster's Timestamp is EPOCH-BASED, which is what Amazon Connect sends
	// and the one thing this fixture used to get wrong: it declared 0 while its
	// own PRODUCER_TIMESTAMP said 1000000000, so the fixture described a stream
	// whose media began 31 years before its producer timestamped it. A corpus
	// that models a timeline origin the field never uses validates the
	// assumption, not the world -- the same defect as building every Cluster
	// known-size. With defaultTimestampScale (1 ms per tick) the tick count is
	// the producer timestamp in milliseconds.
	s.connectFragment(connectFragOpts{
		trackOrder:     []string{trackNameFromCustomer, trackNameToCustomer},
		uidSeed:        0xC0,
		contactID:      fakeContactA,
		fragmentNumber: "connect-real-0",
		producerTS:     epochTag(connectEpochMillis),
		clusterTS:      connectEpochMillis,
		blocks: []trackBlock{
			{1, block{0, pcm(24, 0x41)}},
			{2, block{0, pcm(24, 0x51)}},
			{1, block{10, pcm(24, 0x61)}},
			{2, block{10, pcm(24, 0x71)}},
		},
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
			"occur ONLY after it. The Cluster Timestamp is EPOCH-BASED, as Connect sends it,",
			"and names the same instant as PRODUCER_TIMESTAMP.",
			"This fixture also carries the rest of the Connect PROFILE: Info holds Title and",
			"MuxingApp = WritingApp = the producing SDK's version string; each TrackEntry has",
			"NO Audio master at all and declares CodecID=A_AAC with CodecPrivate 0x1190 while",
			"its payload is 8 kHz 16-bit L16 PCM, which only the MimeType tag states; and the",
			"AUDIO_TO_CUSTOMER / AUDIO_FROM_CUSTOMER tag values are the constants 1 and 2,",
			"which here CONTRADICT the Tracks mapping (track 1 is AUDIO_FROM_CUSTOMER).",
			"All values are fabricated synthetic data.",
		),
		Data: data,
		Facts: Facts{
			Description:        "Real Connect-shaped one-fragment layout: the whole profile, four blocks, four Segment Tags elements.",
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
			Notes:              "Segment-level Tags appear both before and after the Cluster; AWS_KINESISVIDEO_MILLIS_BEHIND_NOW and AWS_KINESISVIDEO_CONTINUATION_TOKEN occur ONLY after it. The Cluster Timestamp is epoch-based (1000000000000 ticks of 1 ms), naming the same instant as PRODUCER_TIMESTAMP, so a consumer reading it as an elapsed media time is visibly wrong here. Models the measured Connect profile: no Audio master under TrackEntry (a consumer reading SamplingFrequency/Channels/BitDepth from the track finds nothing and must take them from the MimeType tag), CodecID=A_AAC with CodecPrivate 0x1190 over L16 PCM (a consumer dispatching a decoder on CodecID decodes noise), and Info carrying Title plus MuxingApp = WritingApp. The AUDIO_*_CUSTOMER tag values are the constants 1 and 2 while Tracks numbers AUDIO_FROM_CUSTOMER first, so a consumer mapping direction from those tag values gets this capture backwards.",
		},
	}
}

// trackOrderSwapped is connect_real_shape's Tracks element the other way round,
// which is the second thing the field produces: track 1 is AUDIO_TO_CUSTOMER in
// some captures and AUDIO_FROM_CUSTOMER in others, and the TrackUID travels with
// the NAME rather than with the number. The AUDIO_*_CUSTOMER tag values stay the
// constants "1" and "2" in both, so they agree with the mapping here and
// contradict it in connect_real_shape. Name is the only authoritative mapping,
// and a corpus with one fixed order would let a consumer hard-code the wrong one.
func trackOrderSwapped() Fixture {
	const clusterTS = connectEpochMillis + 3_600_000
	s := newStream()
	s.connectFragment(connectFragOpts{
		trackOrder:     []string{trackNameToCustomer, trackNameFromCustomer},
		uidSeed:        0xC8,
		contactID:      fakeContactB,
		fragmentNumber: "swapped-0",
		producerTS:     epochTag(clusterTS),
		clusterTS:      clusterTS,
		blocks: []trackBlock{
			{1, block{0, pcm(24, 0x41)}},
			{2, block{0, pcm(32, 0x51)}},
			{1, block{10, pcm(24, 0x61)}},
		},
	})
	data := s.bytes()
	return Fixture{
		Name: "track_order_swapped",
		Comment: joinLines(
			"track_order_swapped: the Connect profile with the Tracks element in the OTHER",
			"order the field produces - track 1 is AUDIO_TO_CUSTOMER and track 2 is",
			"AUDIO_FROM_CUSTOMER, each TrackUID following its NAME. The AUDIO_TO_CUSTOMER /",
			"AUDIO_FROM_CUSTOMER tag values are still the constants 1 and 2, which agree with",
			"this capture's mapping and contradict connect_real_shape's. Track 1 carries 48",
			"PCM bytes in two blocks and track 2 carries 32 in one, so a consumer that maps",
			"direction by track NUMBER, by TrackUID or by those tag values reaches the wrong",
			"channel in one of the two fixtures.",
		),
		Data: data,
		Facts: Facts{
			Description:        "Connect profile with track 1 = AUDIO_TO_CUSTOMER: the reversed Tracks order.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       3,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactB},
			ProducerTimestamps: []string{epochTag(clusterTS)},
			FragmentNumbers:    []string{"swapped-0"},
			Notes:              "Models the measured fact that Connect track order is NOT fixed: track 1 is AUDIO_TO_CUSTOMER here and AUDIO_FROM_CUSTOMER in connect_real_shape, with TrackUID following the name, while the AUDIO_*_CUSTOMER tag values are the constants 1 and 2 in both. A consumer that resolves the caller/agent channel from the track number, the TrackUID or those tag values transcribes one of the two fixtures with the two speakers swapped; only TrackEntry Name distinguishes them.",
		},
	}
}

// shortBlockMidTrack models the block cadence a capture actually has. Connect
// sends 64 ms / 1024-byte SimpleBlocks alternating between the two tracks -- at
// 8 kHz 16-bit mono, 1024 bytes IS 64 ms -- but at least one real fragment holds
// a 192-byte block in the MIDDLE of a track's run. A writer that can only split
// at a fixed size cannot reproduce that, and a reader that derives a block's
// duration from a fixed size, or splits a track's PCM into fixed-size frames,
// mis-times everything after it.
func shortBlockMidTrack() Fixture {
	const clusterTS = connectEpochMillis + 7_200_000
	const fullBlock = 1024 // 64 ms of 8 kHz 16-bit mono PCM
	const shortBlock = 192 // 12 ms, in a 64 ms slot
	s := newStream()
	s.connectFragment(connectFragOpts{
		trackOrder:     []string{trackNameFromCustomer, trackNameToCustomer},
		uidSeed:        0xD0,
		contactID:      fakeContactA,
		fragmentNumber: "short-block-0",
		producerTS:     epochTag(clusterTS),
		clusterTS:      clusterTS,
		blocks: []trackBlock{
			{1, block{0, pcm(fullBlock, 0x01)}},
			{2, block{0, pcm(fullBlock, 0x02)}},
			{1, block{64, pcm(shortBlock, 0x03)}}, // the outlier, mid-track
			{2, block{64, pcm(fullBlock, 0x04)}},
			{1, block{128, pcm(fullBlock, 0x05)}},
			{2, block{128, pcm(fullBlock, 0x06)}},
		},
	})
	data := s.bytes()
	return Fixture{
		Name: "short_block_mid_track",
		Comment: joinLines(
			"short_block_mid_track: the Connect profile with the block cadence a real capture",
			"has - SimpleBlocks alternating between the two tracks at relative timecodes 0,",
			"64 and 128 ms, 1024 PCM bytes each, which at 8 kHz 16-bit mono is exactly 64 ms.",
			"Track 1's MIDDLE block is 192 bytes (12 ms) instead, so its run is 1024/192/1024",
			"while track 2's is 1024/1024/1024 and the two tracks end with different byte",
			"counts inside one Cluster. Block duration follows the block's OWN length; a",
			"fixed-size assumption mis-times every block after the outlier. The TIMECODES",
			"stay on the 0/64/128 ms grid regardless, so track 1's short block is followed",
			"by a 52 ms gap - the capture disagrees with itself the same way, and closing",
			"the gap here would assert a producer behaviour nobody observed.",
		),
		Data: data,
		Facts: Facts{
			Description:        "64 ms / 1024-byte alternating blocks with one 192-byte block mid-track.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       6,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{epochTag(clusterTS)},
			FragmentNumbers:    []string{"short-block-0"},
			Notes:              "Models the measured 64 ms / 1024-byte alternating cadence together with the 192-byte block observed MID-TRACK in a real fragment. A consumer that derives a block's duration from a constant block size, or that reframes a track's PCM into fixed-size chunks, shifts every sample after the outlier; the fixture also gives the two tracks unequal byte counts within one Cluster, which a per-track buffer keyed to a single length cannot hold. The block TIMECODES stay on the 0/64/128 ms grid across the outlier, so the short block leaves a gap between its own 12 ms of PCM and the next block's start: the capture this models carries the same disagreement, and a fixture that renumbered the timecodes to close it would assert a producer behaviour nobody observed.",
		},
	}
}

// taglessTail puts the tagless fragment where the field puts it: at the END of a
// contact's run, with nothing after it. tagless_single and tagless_consecutive
// both keep the tagless fragments mid-stream, where a consumer can attribute
// them by looking at what follows. Here there is nothing to look at, so a
// consumer that defers attribution until the next tagged fragment drops the last
// fragment of every contact.
func taglessTail() Fixture {
	const base = connectEpochMillis + 10_800_000
	tss := []string{epochTag(base), epochTag(base + 1024), epochTag(base + 2048)}
	s := newStream()
	for i := 0; i < 3; i++ {
		o := connectFragOpts{
			trackOrder:     []string{trackNameFromCustomer, trackNameToCustomer},
			uidSeed:        0xD8,
			contactID:      fakeContactA,
			fragmentNumber: fmt.Sprintf("tail-tagless-%d", i),
			producerTS:     tss[i],
			clusterTS:      base + uint64(i)*1024,
			blocks: []trackBlock{
				{1, block{0, pcm(24, byte(0x11+i))}},
				{2, block{0, pcm(24, byte(0x21+i))}},
			},
		}
		// The LAST fragment carries no Tags element at all.
		if i == 2 {
			o.omitTags = true
			o.contactID = ""
		}
		s.connectFragment(o)
	}
	data := s.bytes()
	return Fixture{
		Name: "tagless_tail",
		Comment: joinLines(
			"tagless_tail: 3 Connect-profile fragments sharing one SegmentUUID where the LAST",
			"one omits every Tags element and nothing follows it. The two tagless fixtures",
			"already in the corpus put their tagless fragments mid-stream, where the next",
			"tagged fragment settles attribution; here the stream simply ends, so the final",
			"fragment's audio belongs to the run that preceded it or to nothing at all.",
		),
		Data: data,
		Facts: Facts{
			Description:        "3 fragments; the FINAL fragment is tagless and nothing follows it.",
			Fragments:          3,
			Segments:           3,
			Clusters:           3,
			SimpleBlocks:       6,
			EBMLHeaders:        3,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         []string{fakeContactA, fakeContactA, ""},
			ProducerTimestamps: tss,
			FragmentNumbers:    []string{"tail-tagless-0", "tail-tagless-1", "tail-tagless-2"},
			Notes:              "Models the tagless fragment observed at the TAIL of a contact run, which tagless_single and tagless_consecutive do not cover because both keep theirs mid-stream. A consumer that attributes a tagless fragment by waiting for the next tagged one, or that flushes a contact only when a new ContactId appears, silently drops the last fragment of every contact; attribution must fall back to the preceding run and to end of stream.",
		},
	}
}

// streamReuse models a KVS stream's lifetime as a producer that reuses streams
// gives it: ONE stream carries many separate contacts' runs, spread over a span
// far longer than any one of them, and the stream's own NAME does not name the
// contact its first fragment belongs to. A consumer that opens a stream named
// for a contact and reads from the beginning gets somebody else's audio.
//
// The SHAPE is what a fixture models. How many contacts one stream held, over
// how long, is a measurement of somebody's deployment rather than a property of
// the format, so it is not restated here and nothing depends on it.
func streamReuse() Fixture {
	// The synthetic name of the stream these fragments came from. It names
	// fakeContactA, whose fragments are not in this stream at all.
	const streamName = "connect-contact-00000000-0000-4000-8000-000000000001"
	const firstRun = connectEpochMillis
	// Five days later: the same stream, a different contact, no fragment in between.
	const secondRun = firstRun + 5*24*60*60*1000
	runs := []struct {
		contact string
		uidSeed byte
		base    uint64
		prefix  string
	}{
		{fakeContactB, 0xE0, firstRun, "reuse-b"},
		{fakeContactC, 0xE8, secondRun, "reuse-c"},
	}
	var contacts, tss, fns []string
	s := newStream()
	for _, r := range runs {
		for i := 0; i < 2; i++ {
			clusterTS := r.base + uint64(i)*1024
			fn := fmt.Sprintf("%s-%d", r.prefix, i)
			s.connectFragment(connectFragOpts{
				trackOrder:     []string{trackNameFromCustomer, trackNameToCustomer},
				uidSeed:        r.uidSeed,
				contactID:      r.contact,
				fragmentNumber: fn,
				producerTS:     epochTag(clusterTS),
				clusterTS:      clusterTS,
				blocks: []trackBlock{
					{1, block{0, pcm(24, byte(0x31+i))}},
					{2, block{0, pcm(24, byte(0x41+i))}},
				},
			})
			contacts = append(contacts, r.contact)
			tss = append(tss, epochTag(clusterTS))
			fns = append(fns, fn)
		}
	}
	data := s.bytes()
	return Fixture{
		Name: "stream_reuse",
		Comment: joinLines(
			"stream_reuse: ONE stream holding two different contacts' runs, five days apart,",
			"each with its own SegmentUUID and epoch-based Cluster Timestamps. The stream's",
			"own (synthetic) name is",
			"  "+streamName,
			"which names a THIRD contact whose fragments are not in this stream at all: the",
			"very first fragment already belongs to a different contact. Amazon Connect reuses",
			"streams, so a stream name is not a contact filter and the first fragment is not",
			"the requested contact's first fragment. filter_mismatch covers a contact change",
			"mid-run; this covers the stream outliving every contact in it.",
			"",
			"THE NAME IS NOT IN THESE BYTES, and cannot be: a stream name is an API-level",
			"identifier and Matroska has no field for it. What the fixture models is the half",
			"that IS in the document -- two contacts, days apart, in one stream, the first",
			"fragment belonging to neither the reader's expectation nor the name above. The",
			"name is stated here so the shape is legible; a consumer receives it from its own",
			"GetMedia call, not from the stream.",
		),
		Data: data,
		Facts: Facts{
			Description:        "One reused stream: two contacts' runs five days apart, neither named by the stream.",
			Fragments:          4,
			Segments:           4,
			Clusters:           4,
			SimpleBlocks:       8,
			EBMLHeaders:        4,
			UnknownSizeSegment: true,
			KnownSizeCluster:   false,
			ContactIDs:         contacts,
			ProducerTimestamps: tss,
			FragmentNumbers:    fns,
			Notes:              "Models the reuse of a KVS stream by a producer that does not open a new one per contact: ONE stream carries many separate contacts' runs, spread over a span far longer than any one of them, and the stream's NAME does not name the contact its first fragment belongs to. Here the stream is named for " + fakeContactA + ", which appears in no fragment, and the two runs it does hold are five days apart. A consumer that trusts the stream name, or that treats the first fragment it reads as the requested contact's, transcribes another contact's audio; only the per-fragment ContactId tag selects a contact. The stream NAME is not in the document and cannot be -- it is an API-level identifier with no Matroska field -- so the fixture carries only the half that is expressible in bytes: several contacts, days apart, in one stream. The name is recorded in the fixture's own header comment for legibility.",
		},
	}
}
