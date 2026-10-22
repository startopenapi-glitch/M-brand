package support

import "testing"

func TestValidStatus(t *testing.T) {
	for _, s := range []string{"open", "reviewing", "resolved", "rejected"} {
		if !ValidStatus(s) {
			t.Fatal(s)
		}
	}
	if ValidStatus("closed") {
		t.Fatal("closed")
	}
}
