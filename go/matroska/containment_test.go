package matroska

import (
	"reflect"
	"sort"
	"testing"

	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/parser"
)

// idUnregistered is an ID this registry must never know. It comes from
// internal/ebmltest rather than being picked here, because "surely nobody uses
// this ID" is exactly the assumption that goes stale: these tests used 0xEE
// until the schema check pointed out that 0xEE is Matroska's BlockAddID.
const idUnregistered = ebmltest.UnassignedLeafID

func TestLegalChildren(t *testing.T) {
	segment, ok := LegalChildren(IDSegment)
	if !ok {
		t.Fatal("Segment should have a complete child list")
	}
	wantSegment := []parser.ElementID{
		IDSeekHead, IDInfo, IDCluster, IDTracks, IDCues, IDAttachments, IDChapters, IDTags,
	}
	sort.Slice(wantSegment, func(i, j int) bool { return wantSegment[i] < wantSegment[j] })
	if !reflect.DeepEqual(segment, wantSegment) {
		t.Fatalf("Segment children = %v, want %v", segment, wantSegment)
	}

	cluster, ok := LegalChildren(IDCluster)
	if !ok {
		t.Fatal("Cluster should have a complete child list")
	}
	wantCluster := []parser.ElementID{IDTimestamp, IDPosition, IDPrevSize, IDSimpleBlock, IDBlockGroup}
	sort.Slice(wantCluster, func(i, j int) bool { return wantCluster[i] < wantCluster[j] })
	if !reflect.DeepEqual(cluster, wantCluster) {
		t.Fatalf("Cluster children = %v, want %v", cluster, wantCluster)
	}
	if _, ok := LegalChildren(IDInfo); ok {
		t.Fatal("Info should not claim a complete child list")
	}

	segment[0] = 0
	again, ok := LegalChildren(IDSegment)
	if !ok || again[0] == 0 {
		t.Fatal("LegalChildren returned a mutable registry table")
	}
}

func TestEndsUnknownSizeMaster(t *testing.T) {
	tests := []struct {
		name       string
		open, next parser.ElementID
		want       bool
	}{
		{"Tags ends Cluster", IDCluster, IDTags, true},
		{"SimpleBlock stays in Cluster", IDCluster, IDSimpleBlock, false},
		{"Void stays in Cluster", IDCluster, IDVoid, false},
		{"CRC-32 stays in Cluster", IDCluster, IDCRC32, false},
		{"unregistered stays in Cluster", IDCluster, idUnregistered, false},
		{"Cluster ends Cluster", IDCluster, IDCluster, true},
		{"Cluster stays in Segment", IDSegment, IDCluster, false},
		{"unknown open never ends", idUnregistered, IDTags, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := EndsUnknownSizeMaster(test.open, test.next); got != test.want {
				t.Fatalf("EndsUnknownSizeMaster(%s, %s) = %v, want %v",
					test.open, test.next, got, test.want)
			}
		})
	}

	reg := NewRegistry(Default())
	if err := reg.Register(ElementInfo{ID: idUnregistered, Name: "Vendor", Type: TypeBinary}); err != nil {
		t.Fatalf("Register vendor: %v", err)
	}
	if reg.EndsUnknownSizeMaster(IDCluster, idUnregistered) {
		t.Fatal("a vendor element must never be a containment boundary")
	}
}

func TestContainmentConsistency(t *testing.T) {
	for master, children := range completeChildren {
		if info, ok := Lookup(master); !ok || info.Type != TypeMaster {
			t.Fatalf("containment master %s is not a registered master", master)
		}
		for _, child := range children {
			if _, ok := Lookup(child); !ok {
				t.Fatalf("child %s of %s is not registered", child, master)
			}
			if master == IDSegment {
				if info, _ := Lookup(child); info.Type != TypeMaster {
					t.Errorf("Segment child %s has type %s, want master", child, info.Type)
				}
			}
		}
	}
	for global := range globalElements {
		if _, ok := Lookup(global); !ok {
			t.Fatalf("global element %s is not registered", global)
		}
	}
}
