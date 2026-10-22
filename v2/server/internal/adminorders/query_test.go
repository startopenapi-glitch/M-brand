package adminorders

import (
	"net/url"
	"testing"
	"time"
)

func TestParseFilterBoundaries(t *testing.T) {
	for _, q := range []string{"page=0", "page=100001", "page_size=101", "status=unknown", "sort=amount", "from=2026-02-30", "to=bad", "from=2026-10-22&to=2026-10-21"} {
		v, _ := url.ParseQuery(q)
		if _, e := Parse(v); e == nil {
			t.Errorf("accepted %s", q)
		}
	}
	v, _ := url.ParseQuery("q=%25_&status=paid&from=2026-10-22&to=2026-10-22&page=2&include_mock=1")
	f, e := Parse(v)
	if e != nil || f.Q != "%_" || f.Page != 2 || f.PageSize != 50 || !f.IncludeMock || f.To.Sub(*f.From) != 24*time.Hour {
		t.Fatalf("filter %v %v", f, e)
	}
}
