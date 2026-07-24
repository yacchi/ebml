package fragment

import (
	"github.com/yacchi/ebml-reader/parser"
)

// Assembler drives the streaming EBML cursor over a KVS GetMedia byte stream and
// assembles one Fragment per completed Cluster.
//
// Usage is push-based: feed arbitrary []byte chunks with Feed, which returns any
// Fragments that completed within that chunk, then call Finalize once at EOF to
// close the trailing unknown-size Segment and surface any structural error.
//
// The result is split-invariant: the sequence of Fragments (and their contents)
// is identical regardless of how the input bytes are chunked across Feed calls.
type Assembler struct {
	p      *parser.Parser
	mstack []mframe

	// pendingLeaf is set after a leaf header is consumed but its payload has not
	// yet been fully read (NeedMoreData). The read is retried on the next drain.
	pendingLeaf *pendingLeaf

	// Segment-scoped state, reset whenever a new Segment master is entered.
	segTags   map[string]string
	segTracks []Track

	// Element-scoped temporaries.
	curTagName  string
	curTagValue string
	curCluster  *clusterAccum

	// emitted collects Fragments completed during the current Feed/Finalize call.
	emitted []*Fragment
}

type mframe struct {
	id      uint32
	unknown bool // unknown-size master (a KVS Segment); closed at a top-level boundary or EOF
}

type pendingLeaf struct {
	id uint32
}

type clusterAccum struct {
	timestamp uint64
	blocks    []*parser.SimpleBlock
}

// New returns an Assembler configured with the KVS/Matroska element classifier
// so Segment/Cluster/Tracks/Tags/TrackEntry/SimpleTag classify as masters and
// their leaves decode correctly.
func New() *Assembler {
	return &Assembler{
		p: parser.New(parser.WithKindClassifier(parser.KVSKindForElementID)),
	}
}

// Feed pushes a chunk of the stream into the assembler and returns every
// Fragment whose Cluster completed while consuming it. The returned slice is
// freshly allocated per call (nil when nothing completed) and is owned by the
// caller.
func (a *Assembler) Feed(chunk []byte) ([]*Fragment, error) {
	a.emitted = nil
	a.p.Feed(chunk)
	if err := a.drain(); err != nil {
		return nil, err
	}
	return a.emitted, nil
}

// Finalize closes the stream at EOF. It drains any buffered bytes, then closes
// the trailing unknown-size Segment(s). It returns any Fragments that completed
// during finalization (normally none, since Clusters are known-size and emit
// early) and a structural error if EOF arrived inside a known-size element.
func (a *Assembler) Finalize() ([]*Fragment, error) {
	a.emitted = nil
	if err := a.drain(); err != nil {
		return nil, err
	}
	closed, err := a.p.FinalizeEOF()
	if err != nil {
		return nil, err
	}
	for _, c := range closed {
		id := a.mstack[len(a.mstack)-1].id
		a.mstack = a.mstack[:len(a.mstack)-1]
		if e := a.onCloseMaster(id); e != nil {
			return nil, e
		}
		_ = c
	}
	return a.emitted, nil
}

// drain makes as much structural progress as the buffered bytes allow, returning
// nil when it runs out of data (NeedMoreData) and a non-nil error only on a real
// structural/decoding failure.
func (a *Assembler) drain() error {
	for {
		if a.pendingLeaf != nil {
			payload, err := a.p.ReadPayload()
			if err != nil {
				if isNeedMore(err) {
					return nil
				}
				return err
			}
			id := a.pendingLeaf.id
			a.pendingLeaf = nil
			if err := a.onLeaf(id, payload); err != nil {
				return err
			}
			continue
		}

		h, err := a.p.Peek()
		if err != nil {
			if isNeedMore(err) {
				return nil
			}
			return err
		}

		// A known-size master reached its declared end (e.g. a Cluster): this is
		// the early-emission point.
		if h.Kind == parser.KindEndMaster {
			id := a.mstack[len(a.mstack)-1].id
			if err := a.p.LeaveMaster(); err != nil {
				return err
			}
			a.mstack = a.mstack[:len(a.mstack)-1]
			if err := a.onCloseMaster(id); err != nil {
				return err
			}
			continue
		}

		// An unknown-size Segment is closed structurally when the next top-level
		// element (EBML header or Segment) begins the following fragment. This is
		// driven by element structure via CloseMaster, never by scanning bytes for
		// the EBML magic, so PCM containing the magic cannot cause a spurious split.
		if len(a.mstack) > 0 && a.mstack[len(a.mstack)-1].unknown && parser.IsTopLevelElementID(h.ID) {
			id := a.mstack[len(a.mstack)-1].id
			if err := a.p.CloseMaster(); err != nil {
				return err
			}
			a.mstack = a.mstack[:len(a.mstack)-1]
			if err := a.onCloseMaster(id); err != nil {
				return err
			}
			continue
		}

		if _, err := a.p.ConsumeHeader(); err != nil {
			return err
		}
		if h.Kind == parser.KindMaster {
			if err := a.p.EnterMaster(); err != nil {
				return err
			}
			a.mstack = append(a.mstack, mframe{id: h.ID, unknown: h.Size < 0})
			a.onEnterMaster(h.ID)
		} else {
			a.pendingLeaf = &pendingLeaf{id: h.ID}
		}
	}
}

// parentID returns the ID of the master currently on top of the stack (the
// parent of the element being processed), or 0 when at the top level.
func (a *Assembler) parentID() uint32 {
	if len(a.mstack) == 0 {
		return 0
	}
	return a.mstack[len(a.mstack)-1].id
}

func (a *Assembler) onEnterMaster(id uint32) {
	switch id {
	case parser.ElementIDSegment:
		// New fragment boundary: nothing from the previous Segment may leak in.
		a.segTags = make(map[string]string)
		a.segTracks = nil
		a.curCluster = nil
	case parser.ElementIDTrackEntry:
		a.segTracks = append(a.segTracks, Track{})
	case parser.ElementIDSimpleTag:
		a.curTagName = ""
		a.curTagValue = ""
	case parser.ElementIDCluster:
		a.curCluster = &clusterAccum{}
	}
}

func (a *Assembler) onCloseMaster(id uint32) error {
	switch id {
	case parser.ElementIDSimpleTag:
		if a.curTagName != "" && a.segTags != nil {
			a.segTags[a.curTagName] = a.curTagValue
		}
	case parser.ElementIDCluster:
		a.emitFragment()
		a.curCluster = nil
	}
	return nil
}

func (a *Assembler) onLeaf(id uint32, payload []byte) error {
	parent := a.parentID()
	switch {
	case id == parser.ElementIDTrackNumber && parent == parser.ElementIDTrackEntry:
		v, err := parser.DecodeUint(payload)
		if err != nil {
			return err
		}
		a.lastTrack().Number = v
	case id == parser.ElementIDTrackType && parent == parser.ElementIDTrackEntry:
		v, err := parser.DecodeUint(payload)
		if err != nil {
			return err
		}
		a.lastTrack().Type = v
	case id == parser.ElementIDCodecID && parent == parser.ElementIDTrackEntry:
		a.lastTrack().CodecID = parser.DecodeString(payload)
	case id == parser.ElementIDTagName && parent == parser.ElementIDSimpleTag:
		a.curTagName = parser.DecodeString(payload)
	case id == parser.ElementIDTagString && parent == parser.ElementIDSimpleTag:
		a.curTagValue = parser.DecodeString(payload)
	case id == parser.ElementIDTimestamp && parent == parser.ElementIDCluster:
		if a.curCluster != nil {
			v, err := parser.DecodeUint(payload)
			if err != nil {
				return err
			}
			a.curCluster.timestamp = v
		}
	case id == parser.ElementIDSimpleBlock && parent == parser.ElementIDCluster:
		if a.curCluster != nil {
			block, err := parser.ParseSimpleBlock(payload)
			if err != nil {
				return err
			}
			a.curCluster.blocks = append(a.curCluster.blocks, block)
		}
	}
	return nil
}

// lastTrack returns a pointer to the TrackEntry currently being decoded.
func (a *Assembler) lastTrack() *Track {
	return &a.segTracks[len(a.segTracks)-1]
}

// emitFragment snapshots the current Segment's Tags/Tracks together with the
// just-completed Cluster into a Fragment and appends it to emitted.
func (a *Assembler) emitFragment() {
	if a.curCluster == nil {
		return
	}
	tags := make(map[string]string, len(a.segTags))
	for k, v := range a.segTags {
		tags[k] = v
	}
	tracks := make([]Track, len(a.segTracks))
	copy(tracks, a.segTracks)

	a.emitted = append(a.emitted, &Fragment{
		Tags:             tags,
		Tracks:           tracks,
		ClusterTimestamp: a.curCluster.timestamp,
		Blocks:           a.curCluster.blocks,
	})
}

func isNeedMore(err error) bool {
	_, ok := err.(parser.NeedMoreData)
	return ok
}
