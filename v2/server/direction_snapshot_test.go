package app

import (
	"encoding/json"
	"testing"
)

func TestDirectionSnapshotFreezesPresentation(t *testing.T) {
	f := normalizeForm(BrandForm{"cn_name": "山物", "industry": "精制茶饮", "positioning": "慢生活"})
	s, ok := buildDirectionSnapshot(f, 0)
	if !ok {
		t.Fatal("snapshot not built")
	}
	if s.DirectionID != "A" || s.Title == "" || s.VisualMechanism == "" || s.Summary == "" {
		t.Fatalf("incomplete snapshot: %+v", s)
	}
	if s.Presentation.Format == "" || s.Presentation.MainVisual == "" || s.Presentation.Composition == "" || s.Presentation.Medium == "" {
		t.Fatalf("incomplete presentation: %+v", s.Presentation)
	}
	raw, _ := json.Marshal(s)
	var round SelectedDirectionSnapshot
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if round.DirectionVersion != directionConfigVersion || round.Summary != s.Summary {
		t.Fatal("snapshot roundtrip changed")
	}
}

func TestOldJobWithoutSnapshotFallsBackToChosenDirection(t *testing.T) {
	chosen := 1
	j := jobRow{ID: "OLDJOB", Chosen: &chosen, Form: normalizeForm(BrandForm{"cn_name": "旧品牌", "industry": "女装"})}
	s, ok := selectedSnapshot(j)
	if !ok || s.DirectionID != "B" || s.Title == "" {
		t.Fatalf("fallback snapshot=%+v ok=%v", s, ok)
	}
	if j.DirectionSnapshot != nil {
		t.Fatal("fallback must not write a snapshot")
	}
}

func TestStructuredLogoDirectionSnapshot(t *testing.T) {
	f := normalizeForm(BrandForm{"cn_name": "山物", "industry": "茶饮"})
	dirs := fallbackLogoDirections(f)
	snap := buildLogoDirectionSnapshot(dirs[0])
	if snap.StructuredDirection == nil || snap.StructuredDirection.Title != dirs[0].Title {
		t.Fatalf("structured direction missing: %+v", snap)
	}
	if snap.Summary == "" || snap.DirectionID == "" || snap.Presentation.Format == "" {
		t.Fatalf("incomplete structured snapshot: %+v", snap)
	}
	b, _ := json.Marshal(snap)
	var round SelectedDirectionSnapshot
	if err := json.Unmarshal(b, &round); err != nil || round.StructuredDirection == nil {
		t.Fatalf("structured snapshot roundtrip: %v %+v", err, round)
	}
}
