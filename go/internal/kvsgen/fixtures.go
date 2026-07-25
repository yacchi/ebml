package kvsgen

import (
	"fmt"

	"github.com/yacchi/ebml-reader/matroska"
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
		multiCluster(),
		multiSegment(),
		taglessSingle(),
		taglessConsecutive(),
		filterMismatch(),
		gap(),
		falseEBMLMagicInPCM(),
		tailLastFragment(),
		scaledTimestamps(),
		unknownElements(),
	}
}

// scaledTimestamps is the fixture that makes timestamp scaling observable: its
// Info declares a TimestampScale of 100_000 ns instead of the 1_000_000 default,
// and its single known-size Cluster carries a non-zero Timestamp plus a
// SimpleBlock whose relative timecode is NEGATIVE, which is legal and places that
// block before its Cluster's timestamp. A consumer that forgets the scale, or
// that reads the relative timecode as unsigned, cannot reproduce these times.
func scaledTimestamps() Fixture {
	const scale = 100000
	data := concat(
		ebmlHeader(),
		elemUnknown(matroska.IDSegment, concat(
			infoElement(segmentUID(0xA0), scale),
			tracksElement(false),
			tagsElement("8000000000.000", "scaled-0", fakeContactA),
			clusterElement(1000,
				simpleBlock(-20, pcm(24, 0x11)),
				simpleBlock(0, pcm(24, 0x12)),
				simpleBlock(20, pcm(24, 0x13)),
			),
		)),
	)
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
			KnownSizeCluster:   true,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"8000000000.000"},
			FragmentNumbers:    []string{"scaled-0"},
			Notes:              "TimestampScale=100000; block timecodes -20/0/+20 around Cluster Timestamp 1000.",
		},
	}
}

// unknownElements is the fixture that proves an element no registry knows costs
// nothing: the Segment carries an unregistered LEAF (0xEE, a decodable uint) and
// an unregistered MASTER-shaped element (0x4FFF) holding two ordinary child
// leaves. With the standard registry the cursor reads 0x4FFF as one opaque binary
// leaf whose bytes stay complete; registering it as a master is what makes those
// children nest as elements of their own.
func unknownElements() Fixture {
	data := concat(
		ebmlHeader(),
		elemUnknown(matroska.IDSegment, concat(
			infoElement(segmentUID(0xB0), defaultTimestampScale),
			elem(idUnregisteredLeaf, encodeUint(42)),
			elem(idUnregisteredMaster, concat(
				elem(matroska.IDName, []byte("vendor-box")),
				elem(matroska.IDTrackNumber, encodeUint(7)),
			)),
			tracksElement(false),
			tagsElement("9000000000.000", "unknown-0", fakeContactA),
			clusterElement(0, simpleBlock(0, pcm(24, 0x21))),
		)),
	)
	return Fixture{
		Name: "unknown_elements",
		Comment: joinLines(
			"unknown_elements: ONE fragment whose Segment carries two elements no registry",
			"knows: the LEAF 0xEE (payload 0x2A = 42, a decodable uint) and the",
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
			KnownSizeCluster:   true,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"9000000000.000"},
			FragmentNumbers:    []string{"unknown-0"},
			Notes:              "0xEE leaf and 0x4FFF master-shaped element are unregistered; the reader never breaks on them.",
		},
	}
}

func multiCluster() Fixture {
	data := concat(
		ebmlHeader(),
		elemUnknown(matroska.IDSegment, concat(
			infoElement(segmentUID(0x20), defaultTimestampScale),
			tracksElement(false),
			tagsElement("1500000000.000", "multi-cluster", fakeContactA),
			clusterElement(0,
				simpleBlock(0, pcm(24, 0x61)),
				simpleBlock(10, pcm(24, 0x62)),
			),
			clusterElement(1024,
				simpleBlock(0, pcm(24, 0x71)),
				simpleBlock(10, pcm(24, 0x72)),
			),
		)),
	)
	return Fixture{
		Name: "multi_cluster",
		Comment: joinLines(
			"multi_cluster: ONE EBML header + ONE unknown-size Segment containing",
			"Info, Tracks, Tags, and TWO known-size Clusters. Each Cluster has a Timestamp",
			"and two SimpleBlocks. Both Cluster end_master events occur before the",
			"Segment is closed by FinalizeEOF.",
		),
		Data: data,
		Facts: Facts{
			Description:        "One unknown-size Segment containing two known-size Clusters.",
			Fragments:          1,
			Segments:           1,
			Clusters:           2,
			SimpleBlocks:       4,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   true,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"1500000000.000"},
			FragmentNumbers:    []string{"multi-cluster"},
			Notes:              "Both Clusters close structurally before the Segment closes at EOF.",
		},
	}
}

func topologyBasic() Fixture {
	data := fragment(fragOpts{
		producerTS:     "1000000000.000",
		fragmentNumber: "91343852333181000000000000000000000000000000001",
		contactID:      fakeContactA,
		includeInfo:    true,
		audioParams:    true,
		uidSeed:        0x10,
		clusterTS:      0,
		blocks: [][]byte{
			simpleBlock(0, pcm(32, 0x01)),
			simpleBlock(10, pcm(32, 0x02)),
			simpleBlock(20, pcm(32, 0x03)),
		},
	})
	return Fixture{
		Name: "topology_basic",
		Comment: joinLines(
			"topology_basic: ONE EBML header + ONE unknown-size Segment.",
			"Segment { Info, Tracks, Tags(ContactId=A), Cluster(known-size, 3 SimpleBlocks) }.",
			"Property: the Cluster end_master fires (peek) as soon as its known size is",
			"consumed, while the Segment stays open and is closed only by FinalizeEOF.",
			"This is the tail-fix property (last fragment need not wait for connection EOF",
			"to observe the Cluster).",
		),
		Data: data,
		Facts: Facts{
			Description:        "Single fragment: unknown-size Segment holding a known-size Cluster.",
			Fragments:          1,
			Segments:           1,
			Clusters:           1,
			SimpleBlocks:       3,
			EBMLHeaders:        1,
			UnknownSizeSegment: true,
			KnownSizeCluster:   true,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"1000000000.000"},
			FragmentNumbers:    []string{"91343852333181000000000000000000000000000000001"},
			Notes:              "Cluster closes via end_master; Segment closes only at EOF.",
		},
	}
}

func multiSegment() Fixture {
	var data []byte
	tss := []string{"1000000000.000", "1000000001.024", "1000000002.048", "1000000003.072"}
	fns := []string{"...001", "...002", "...003", "...004"}
	for i := 0; i < 4; i++ {
		data = append(data, fragment(fragOpts{
			producerTS:     tss[i],
			fragmentNumber: fmt.Sprintf("9134385233318100000000000000000000000000000000%d", i+1),
			contactID:      fakeContactA,
			includeInfo:    i == 0,
			uidSeed:        0x30,
			clusterTS:      uint64(i) * 1024,
			blocks: [][]byte{
				simpleBlock(0, pcm(32, byte(0x10+i))),
				simpleBlock(10, pcm(32, byte(0x20+i))),
			},
		})...)
	}
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
			KnownSizeCluster:   true,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: tss,
			FragmentNumbers:    fns,
			Notes:              "Segment boundaries detected via top-level EBML sibling, not magic scan.",
		},
	}
}

func taglessSingle() Fixture {
	var data []byte
	// fragment 0: normal; fragment 1: NO Tags element at all; fragment 2: normal.
	data = append(data, fragment(fragOpts{
		producerTS: "2000000000.000", fragmentNumber: "tag-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x40, clusterTS: 0,
		blocks: [][]byte{simpleBlock(0, pcm(24, 0x01))},
	})...)
	data = append(data, fragment(fragOpts{
		producerTS: "2000000001.024", fragmentNumber: "tag-1", omitTags: true,
		clusterTS: 1024, blocks: [][]byte{simpleBlock(0, pcm(24, 0x02))},
	})...)
	data = append(data, fragment(fragOpts{
		producerTS: "2000000002.048", fragmentNumber: "tag-2", contactID: fakeContactA,
		clusterTS: 2048, blocks: [][]byte{simpleBlock(0, pcm(24, 0x03))},
	})...)
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
			KnownSizeCluster:   true,
			ContactIDs:         []string{fakeContactA, "", fakeContactA},
			ProducerTimestamps: []string{"2000000000.000", "2000000001.024", "2000000002.048"},
			FragmentNumbers:    []string{"tag-0", "tag-1", "tag-2"},
			Notes:              "Middle fragment tagless; cursor still parses cleanly.",
		},
	}
}

func taglessConsecutive() Fixture {
	var data []byte
	// fragment 0: normal; fragments 1 and 2: tagless; fragment 3: normal.
	data = append(data, fragment(fragOpts{
		producerTS: "3000000000.000", fragmentNumber: "c-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x50, clusterTS: 0, blocks: [][]byte{simpleBlock(0, pcm(24, 0x01))},
	})...)
	data = append(data, fragment(fragOpts{
		producerTS: "3000000001.024", fragmentNumber: "c-1", omitTags: true,
		includeInfo: true, uidSeed: 0x50,
		clusterTS: 1024, blocks: [][]byte{simpleBlock(0, pcm(24, 0x02))},
	})...)
	data = append(data, fragment(fragOpts{
		producerTS: "3000000002.048", fragmentNumber: "c-2", omitTags: true,
		includeInfo: true, uidSeed: 0x50,
		clusterTS: 2048, blocks: [][]byte{simpleBlock(0, pcm(24, 0x03))},
	})...)
	data = append(data, fragment(fragOpts{
		producerTS: "3000000003.072", fragmentNumber: "c-3", contactID: fakeContactA,
		clusterTS: 3072, blocks: [][]byte{simpleBlock(0, pcm(24, 0x04))},
	})...)
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
			KnownSizeCluster:   true,
			ContactIDs:         []string{fakeContactA, "", "", fakeContactA},
			ProducerTimestamps: []string{"3000000000.000", "3000000001.024", "3000000002.048", "3000000003.072"},
			FragmentNumbers:    []string{"c-0", "c-1", "c-2", "c-3"},
			Notes:              "Two consecutive tagless fragments; cursor parses cleanly.",
		},
	}
}

func filterMismatch() Fixture {
	var data []byte
	// fragments 0,1 = ContactA; fragment 2,3 switch to ContactB (transfer-style).
	ids := []string{fakeContactA, fakeContactA, fakeContactB, fakeContactB}
	tss := []string{"4000000000.000", "4000000001.024", "4000000002.048", "4000000003.072"}
	for i := 0; i < 4; i++ {
		data = append(data, fragment(fragOpts{
			producerTS: tss[i], fragmentNumber: fmt.Sprintf("fm-%d", i), contactID: ids[i],
			includeInfo: i == 0, uidSeed: 0x60, clusterTS: uint64(i) * 1024,
			blocks: [][]byte{simpleBlock(0, pcm(24, byte(0x30+i)))},
		})...)
	}
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
			KnownSizeCluster:   true,
			ContactIDs:         ids,
			ProducerTimestamps: tss,
			FragmentNumbers:    []string{"fm-0", "fm-1", "fm-2", "fm-3"},
			Notes:              "Contact boundary is reader policy; structure is uniform.",
		},
	}
}

func gap() Fixture {
	var data []byte
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
	for i, s := range specs {
		data = append(data, fragment(fragOpts{
			producerTS: s.ts, fragmentNumber: s.fn, contactID: fakeContactA,
			includeInfo: i == 0, uidSeed: 0x70, clusterTS: s.tc,
			blocks: [][]byte{simpleBlock(0, pcm(24, byte(0x50+i)))},
		})...)
	}
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
			KnownSizeCluster:   true,
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
	data := fragment(fragOpts{
		producerTS: "6000000000.000", fragmentNumber: "magic-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x80, clusterTS: 0,
		blocks: [][]byte{
			simpleBlock(0, pcm(24, 0x01)),
			simpleBlock(10, magicPCM), // contains the EBML magic bytes
			simpleBlock(20, pcm(24, 0x03)),
		},
	})
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
			KnownSizeCluster:   true,
			ContactIDs:         []string{fakeContactA},
			ProducerTimestamps: []string{"6000000000.000"},
			FragmentNumbers:    []string{"magic-0"},
			Notes:              "Proves size-driven parsing beats the byte-scan heuristic.",
		},
	}
}

func tailLastFragment() Fixture {
	var data []byte
	// Two fragments; the LAST has no following Segment. Its Cluster completes and
	// is observable via end_master before any EOF; the Segment closes at EOF only.
	data = append(data, fragment(fragOpts{
		producerTS: "7000000000.000", fragmentNumber: "tail-0", contactID: fakeContactA,
		includeInfo: true, uidSeed: 0x90, clusterTS: 0, blocks: [][]byte{simpleBlock(0, pcm(24, 0x01))},
	})...)
	data = append(data, fragment(fragOpts{
		producerTS: "7000000001.024", fragmentNumber: "tail-1", contactID: fakeContactA,
		clusterTS: 1024, blocks: [][]byte{
			simpleBlock(0, pcm(24, 0x02)),
			simpleBlock(10, pcm(24, 0x03)),
		},
	})...)
	return Fixture{
		Name: "tail_last_fragment",
		Comment: joinLines(
			"tail_last_fragment: 2 fragments; the final Segment has NO following Segment.",
			"Its known-size Cluster completes and is observable via end_master BEFORE any",
			"EOF; the unknown-size Segment is closed only by FinalizeEOF. Proves the last",
			"fragment's audio emits without waiting for the connection EOF (no ~4.3s tail).",
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
			KnownSizeCluster:   true,
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
