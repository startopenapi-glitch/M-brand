package analytics

import "testing"

func TestVisitorHash(t *testing.T) {
	if VisitorHash("") != "" {
		t.Fatal("empty")
	}
	a := VisitorHash("v1")
	if a == VisitorHash("v2") || len(a) != 64 {
		t.Fatal("hash")
	}
	if a != VisitorHash("v1") {
		t.Fatal("unstable")
	}
}
