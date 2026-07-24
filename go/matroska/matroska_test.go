package matroska

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yacchi/ebml-reader/parser"
)

func TestLookupWellKnownElements(t *testing.T) {
	tests := []struct {
		id   parser.ElementID
		name string
		typ  ValueType
	}{
		{0x1A45DFA3, "EBML", TypeMaster},
		{0x4282, "DocType", TypeString},
		{0x4461, "DateUTC", TypeDate},
		{0x4489, "Duration", TypeFloat},
		{0x73A4, "SegmentUUID", TypeBinary},
		{0x1654AE6B, "Tracks", TypeMaster},
		{0x536E, "Name", TypeUTF8},
		{0xB5, "SamplingFrequency", TypeFloat},
		{0x1F43B675, "Cluster", TypeMaster},
		{0xA3, "SimpleBlock", TypeBlock},
		{0xFB, "ReferenceBlock", TypeInt},
		{0x1C53BB6B, "Cues", TypeMaster},
		{0x1254C367, "Tags", TypeMaster},
		{0x4487, "TagString", TypeUTF8},
		{0x4660, "FileMediaType", TypeString},
		{0x465C, "FileData", TypeBinary},
		{0x46AE, "FileUID", TypeUint},
	}
	for _, test := range tests {
		got, ok := Lookup(test.id)
		if !ok {
			t.Errorf("Lookup(%s) = miss", test.id)
			continue
		}
		if got.ID != test.id || got.Name != test.name || got.Type != test.typ {
			t.Errorf("Lookup(%s) = %#v, want name %q type %v", test.id, got, test.name, test.typ)
		}
	}
}

func TestClassifierMatchesRegistry(t *testing.T) {
	for id, info := range elements {
		got := KindForElementID(id)
		if info.Type == TypeMaster {
			if got != parser.KindMaster {
				t.Errorf("%s classified as %q, want master", info.Name, got)
			}
		} else if got == parser.KindMaster {
			t.Errorf("%s classified as master despite being a leaf", info.Name)
		}
	}
	if got := KindForElementID(0xFFFFFFFF); got != parser.KindBinary {
		t.Errorf("unknown ID classified as %q, want binary", got)
	}
}

// TestSpecialBinaryLeaves pins the two "skip me" elements the cursor must treat
// as opaque binary leaves (their bytes are never validated or interpreted).
func TestSpecialBinaryLeaves(t *testing.T) {
	for _, tt := range []struct {
		id   parser.ElementID
		name string
	}{
		{0xBF, "CRC-32"},
		{0xEC, "Void"},
	} {
		if got := NameForID(tt.id); got != tt.name {
			t.Errorf("NameForID(%s) = %q, want %q", tt.id, got, tt.name)
		}
		if got := KindForElementID(tt.id); got != parser.KindBinary {
			t.Errorf("KindForElementID(%s) = %q, want %q", tt.id, got, parser.KindBinary)
		}
	}
}

// TestLegacyNameAliases pins the compatibility shim: IDForName accepts the
// well-known pre-RFC 9559 names, but the canonical RFC name is the only one the
// registry ever reports back.
func TestLegacyNameAliases(t *testing.T) {
	for legacy, canonical := range map[string]string{
		"SegmentUID":    "SegmentUUID",
		"FileMimeType":  "FileMediaType",
		"Timecode":      "Timestamp",
		"TimecodeScale": "TimestampScale",
	} {
		canonicalID, ok := IDForName(canonical)
		if !ok {
			t.Errorf("IDForName(%q) = miss, want the canonical name to resolve", canonical)
			continue
		}
		legacyID, ok := IDForName(legacy)
		if !ok {
			t.Errorf("IDForName(%q) = miss, want the legacy alias to resolve", legacy)
			continue
		}
		if legacyID != canonicalID {
			t.Errorf("IDForName(%q) = %s, want %s (same as %q)", legacy, legacyID, canonicalID, canonical)
		}
		if got := NameForID(canonicalID); got != canonical {
			t.Errorf("NameForID(%s) = %q, want the canonical %q", canonicalID, got, canonical)
		}
	}
	// Aliases must not leak into the enumerated registry.
	for _, info := range Elements() {
		if _, isAlias := legacyNames[info.Name]; isAlias {
			t.Errorf("Elements() exposes legacy alias %q as an element name", info.Name)
		}
	}
	if _, ok := IDForName("NotAnElement"); ok {
		t.Error("IDForName resolved an unknown name")
	}
}

func TestLookupMiss(t *testing.T) {
	if _, ok := Lookup(0xDEADBEEF); ok {
		t.Fatal("Lookup returned a match for an unknown ID")
	}
	if got := NameForID(0xDEADBEEF); got != "" {
		t.Fatalf("NameForID unknown ID = %q, want empty", got)
	}
}

func TestParserSmoke(t *testing.T) {
	raw, err := loadHexFixture(filepath.Join("..", "..", "fixtures", "tiny.ebml.hex"))
	if err != nil {
		t.Fatal(err)
	}

	p := parser.New(parser.WithKindClassifier(KindForElementID))
	p.Feed(raw)
	for {
		h, err := p.Peek()
		if err != nil {
			if _, ok := err.(parser.NeedMoreData); ok {
				break
			}
			t.Fatalf("Peek: %v", err)
		}
		if h.Kind == parser.KindEndMaster {
			if err := p.LeaveMaster(); err != nil {
				t.Fatalf("LeaveMaster: %v", err)
			}
			continue
		}
		h, err = p.ConsumeHeader()
		if err != nil {
			t.Fatalf("ConsumeHeader: %v", err)
		}
		if h.Kind == parser.KindMaster {
			if err := p.EnterMaster(); err != nil {
				t.Fatalf("EnterMaster: %v", err)
			}
		} else if err := p.SkipPayload(); err != nil {
			t.Fatalf("SkipPayload: %v", err)
		}
	}
	if _, err := p.FinalizeEOF(); err != nil {
		t.Fatalf("FinalizeEOF: %v", err)
	}
}

func loadHexFixture(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var compact strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		compact.WriteString(strings.Join(strings.Fields(line), ""))
	}
	return hex.DecodeString(compact.String())
}
