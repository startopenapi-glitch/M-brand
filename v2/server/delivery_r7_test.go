package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestDeliveryR7InvalidLocalReferenceNeverBecomesUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "missing", path: "missing.png"},
		{name: "corrupt", path: "corrupt.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer upstream.Close()
			t.Setenv("ZEROBRAND_IMG_BASE_URL", upstream.URL)
			t.Setenv("ZEROBRAND_IMG_API_KEY", "local-fake")
			t.Setenv("ZEROBRAND_IMG_FALLBACK_BASE_URL", "")
			t.Setenv("ZEROBRAND_IMG_FALLBACK_API_KEY", "")
			t.Cleanup(noteImgSuccess)
			ref := filepath.Join(t.TempDir(), tc.path)
			if tc.name == "corrupt" {
				if err := os.WriteFile(ref, []byte("not an image"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := GenerateSize("fake", filepath.Join(t.TempDir(), "out.png"), []string{ref}, "")
			if err == nil || errors.Is(err, errGenerationUnknown) || calls.Load() != 0 {
				t.Fatalf("preflight failure was treated as upstream-unknown: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}
