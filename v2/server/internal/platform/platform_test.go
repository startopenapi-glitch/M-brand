package platform

import (
	"strings"
	"testing"
)

func TestIDsAndConfigDefaults(t *testing.T) {
	if got := NewID(12); len(got) != 12 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("bad id %q", got)
	}
	if got := RandHex(8); len(got) != 16 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("bad hex %q", got)
	}
	t.Setenv("ZEROBRAND_PORT", "")
	t.Setenv("ZEROBRAND_PUBLIC_ORIGIN", "")
	cfg := LoadConfig()
	if cfg.Port != "8830" || cfg.PublicOrigin != "https://localhost:8443" {
		t.Fatalf("bad defaults %+v", cfg)
	}
	t.Setenv("ZEROBRAND_PORT", "9999")
	t.Setenv("ZEROBRAND_PUBLIC_ORIGIN", "https://example.test")
	t.Setenv("ZEROBRAND_GEN_DIR", "/tmp/gen-test")
	cfg = LoadConfig()
	if cfg.Port != "9999" || cfg.PublicOrigin != "https://example.test" || cfg.GenDir != "/tmp/gen-test" {
		t.Fatalf("bad config %+v", cfg)
	}
}
