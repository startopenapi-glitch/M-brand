package app

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanBuildRequiredPublicAssets(t *testing.T) {
	r := buildRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", basePath+"/docs/user-agreement", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "用户服务协议") || !strings.Contains(w.Body.String(), `id="s5"`) {
		t.Fatal("agreement template missing")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", basePath+"/docs/refund", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != basePath+"/docs/user-agreement#s5" {
		t.Fatal("refund link missing")
	}
	for _, name := range []string{"hero", "logo", "system", "applications"} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", basePath+"/assets/examples/m-brand/"+name+".svg", nil))
			var document struct{ XMLName xml.Name }
			if w.Code != 200 || xml.Unmarshal(w.Body.Bytes(), &document) != nil || document.XMLName.Local != "svg" {
				t.Fatalf("embedded image missing: %d", w.Code)
			}
		})
	}
}
