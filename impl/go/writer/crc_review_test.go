package writer_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/writer"
)

// TestChecksumOptionDoesNotLeakToAnUncheckedSibling proves masterOptions is truly
// per-call state, not something that could survive across StartMaster/EndMaster
// cycles on the same Writer: a checksummed master immediately followed, at the
// SAME depth, by a sibling master that passes no MasterOption at all must leave
// the sibling exactly as TestMasterWithoutTheOptionEmitsNoChecksum expects in
// isolation -- but that test never exercises a Writer that has ALREADY produced a
// checksum. A bug that reused or failed to reset masterOptions between frames
// (a package-level or Writer-level field instead of a fresh value per StartMaster)
// would pass every existing checksum test, each of which opens a fresh Writer, and
// only show up here.
func TestChecksumOptionDoesNotLeakToAnUncheckedSibling(t *testing.T) {
	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idRoot, writer.Buffered()); err != nil {
			return err
		}
		if err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
			return err
		}
		if err := w.Uint(idUintL, 1); err != nil {
			return err
		}
		if err := w.EndMaster(); err != nil { // idBranch, checksummed
			return err
		}
		// A second, sibling idBranch at the same depth, with NO MasterOption. If
		// masterOptions (or its checksum/crcID fields) survived from the previous
		// StartMaster call instead of starting fresh, this master would pick up a
		// phantom checksum it never asked for.
		if err := w.StartMaster(idBranch, writer.Buffered()); err != nil {
			return err
		}
		if err := w.Uint(idUintL, 2); err != nil {
			return err
		}
		if err := w.EndMaster(); err != nil { // idBranch, unchecked
			return err
		}
		return w.EndMaster() // idRoot
	})

	events := scan(t, doc, 0)
	var branches []event
	for _, e := range events {
		if e.op == "master" && e.id == idBranch {
			branches = append(branches, e)
		}
	}
	if len(branches) != 2 {
		t.Fatalf("want 2 idBranch masters, got %d: %v", len(branches), eventStrings(events))
	}
	first, second := branches[0], branches[1]

	firstPayload := doc[first.offset+int64(first.headerLen) : first.end]
	if !bytes.HasPrefix(firstPayload, checksumHeader()) {
		t.Fatalf("first idBranch payload %X does not start with the CRC-32 header, want it checksummed", firstPayload)
	}

	secondPayload := doc[second.offset+int64(second.headerLen) : second.end]
	if bytes.HasPrefix(secondPayload, checksumHeader()) {
		t.Fatalf("second idBranch payload %X carries a CRC-32 header, want none: the option leaked from its checksummed sibling", secondPayload)
	}
	// The unchecked sibling must be exactly one Uint leaf, nothing prepended. scan
	// emits a "leaf" event and a separate "payload" event per element, so only the
	// "leaf" events are counted here.
	sub := scan(t, secondPayload, 0)
	var subLeaves []event
	for _, e := range sub {
		if e.op == "leaf" {
			subLeaves = append(subLeaves, e)
		}
	}
	if len(subLeaves) != 1 || subLeaves[0].id != idUintL {
		t.Fatalf("unchecked sibling's children are %v, want exactly one %s leaf", eventStrings(sub), idUintL)
	}
}

// TestChecksumValidationOrderIsIDBeforeStrategyAndBothArePreWrite exercises the
// combination none of the existing tests do: WithChecksum given BOTH an ill-formed
// ID AND a strategy that cannot buffer, on the same StartMaster call. Existing
// tests (TestChecksumRejectsStrategiesThatDoNotBuffer,
// TestChecksumRejectsIllFormedChecksumID) each vary exactly one axis with the
// other held valid, so neither proves anything about applyMasterOptions' actual
// check order -- and a caller debugging a real double-mistake call sees whichever
// error wins. This pins that order (ID checked before strategy, per
// applyMasterOptions' source order) as a regression guard: if it silently
// flipped, this test would catch a *ChecksumStrategyError arriving where an
// *InvalidIDError is documented to win, or vice versa. It also re-confirms, for
// this doubly-invalid combination specifically, that nothing reached the sink.
func TestChecksumValidationOrderIsIDBeforeStrategyAndBothArePreWrite(t *testing.T) {
	const badID parser.ElementID = 0x00 // no VINT length marker at all

	sink := &memSink{}
	w := writer.New(sink)

	// UnknownSize cannot buffer (fails the strategy check) AND badID is ill-formed
	// (fails the ID check): both conditions inside applyMasterOptions fire at once.
	err := w.StartMaster(idBranch, writer.UnknownSize(), writer.WithChecksum(badID))

	var idErr *writer.InvalidIDError
	var strategyErr *writer.ChecksumStrategyError
	switch {
	case errors.As(err, &idErr):
		if idErr.ID != badID {
			t.Fatalf("error names ID %s, want the checksum ID %s", idErr.ID, badID)
		}
	case errors.As(err, &strategyErr):
		t.Fatalf("StartMaster returned *ChecksumStrategyError %v for a doubly-invalid call; "+
			"applyMasterOptions checks ValidID(crcID) before size.kind, so *InvalidIDError must win here. "+
			"Either the check order changed or this is a real defect.", err)
	default:
		t.Fatalf("StartMaster: %v, want *InvalidIDError or *ChecksumStrategyError", err)
	}

	if len(sink.b) != 0 {
		t.Fatalf("sink received %d bytes, want none: a doubly-invalid call must still write nothing", len(sink.b))
	}
	if w.Depth() != 0 {
		t.Fatalf("Depth is %d, want 0: the rejected master must not be left open", w.Depth())
	}

	// The Writer must still be usable afterward.
	if err := w.StartMaster(idBranch, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster after the rejection: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster after the rejection: %v", err)
	}
}

// TestSequentialTopLevelChecksummedMastersDoNotCrossContaminate writes TWO
// independent, checksummed, top-level masters one after another into the SAME
// document and verifies each one's stored CRC-32 covers only ITS OWN children --
// not the other master's bytes, and not bytes carried over from a stale buffer.
// Every existing checksum test builds exactly one checksummed master per
// document, so a bug that reused a single frame.buf across top-level siblings (or
// otherwise let bytes from the first master leak into the second's coverage --
// for instance by mis-sizing a slice reset) would pass all of them and only be
// caught by comparing two independently-built masters against a single
// concatenated document that contains both.
func TestSequentialTopLevelChecksummedMastersDoNotCrossContaminate(t *testing.T) {
	build := func(w *writer.Writer, v uint64, s string) error {
		if err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
			return err
		}
		if err := w.Uint(idUintL, v); err != nil {
			return err
		}
		if err := w.String(idStrL, s); err != nil {
			return err
		}
		return w.EndMaster()
	}

	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := build(w, 1, "first"); err != nil {
			return err
		}
		return build(w, 2, "second")
	})

	events := scan(t, doc, 0)
	var branches []event
	for _, e := range events {
		if e.op == "master" && e.id == idBranch {
			branches = append(branches, e)
		}
	}
	if len(branches) != 2 {
		t.Fatalf("want 2 top-level idBranch masters, got %d: %v", len(branches), eventStrings(events))
	}

	firstWant := buildDoc(t, func(w *writer.Writer) error {
		if err := w.Uint(idUintL, 1); err != nil {
			return err
		}
		return w.String(idStrL, "first")
	})
	secondWant := buildDoc(t, func(w *writer.Writer) error {
		if err := w.Uint(idUintL, 2); err != nil {
			return err
		}
		return w.String(idStrL, "second")
	})

	firstPayload := doc[branches[0].offset+int64(branches[0].headerLen) : branches[0].end]
	firstCovered := verifyChecksummedMaster(t, "first idBranch", firstPayload)
	if !bytes.Equal(firstCovered, firstWant) {
		t.Fatalf("first master covered %X, want %X", firstCovered, firstWant)
	}

	secondPayload := doc[branches[1].offset+int64(branches[1].headerLen) : branches[1].end]
	secondCovered := verifyChecksummedMaster(t, "second idBranch", secondPayload)
	if !bytes.Equal(secondCovered, secondWant) {
		t.Fatalf("second master covered %X, want %X: contains bytes from the first master or a stale buffer", secondCovered, secondWant)
	}
}
