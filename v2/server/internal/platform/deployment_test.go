package platform

import (
	"strings"
	"testing"
)

func TestDeploymentRejectsLegacyOrUnidentifiedConfiguration(t *testing.T) {
	cases := []struct {
		name, id, dsn, origin string
		allow                 bool
	}{
		{"missing identity", "", "postgres://user:private-password@localhost/mbrand", "https://localhost:8443", false},
		{"wrong identity", "0brand", "postgres://user:private-password@localhost/mbrand", "https://localhost:8443", false},
		{"no database fallback", "m-brand", "", "https://localhost:8443", false},
		{"legacy database", "m-brand", "postgres://user:private-password@localhost/zerobrand", "https://localhost:8443", false},
		{"query overrides database", "m-brand", "postgres://user:private-password@localhost/mbrand?dbname=zerobrand", "https://localhost:8443", false},
		{"legacy origin", "m-brand", "postgres://user:private-password@localhost/mbrand", "https://www.0-brand.com", false},
		{"legacy subdomain and trailing dot", "m-brand", "postgres://user:private-password@localhost/mbrand", "https://ADMIN.0-BRAND.COM.:8443", false},
		{"malformed database", "m-brand", "postgres://user:private-password@%broken/other", "https://localhost:8443", false},
		{"own deployment", "m-brand", "postgres://user:private-password@localhost/mbrand", "https://brand.example.com", true},
		{"isolated tests", "m-brand", "postgres://user:private-password@127.0.0.1/zerobrand_test", "https://localhost:8443", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateDeploymentConfig(c.id, c.dsn, c.origin)
			if c.allow && err != nil {
				t.Fatal(err)
			}
			if !c.allow && err == nil {
				t.Fatal("unsafe deployment configuration accepted")
			}
			if err != nil && strings.Contains(err.Error(), "private-password") {
				t.Fatal("database credentials exposed")
			}
		})
	}
}
