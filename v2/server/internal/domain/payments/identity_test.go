package payments

import "testing"

func TestValidIdentity(t *testing.T) {
	if !ValidIdentity("m", "a", "m", "a", "tx") {
		t.Fatal("valid")
	}
	if ValidIdentity("x", "a", "m", "a", "tx") {
		t.Fatal("mchid")
	}
	if ValidIdentity("m", "a", "m", "a", "") {
		t.Fatal("txid")
	}
	if !ValidIdentity("m", "a", "", "", "tx") {
		t.Fatal("unpinned")
	}
}
