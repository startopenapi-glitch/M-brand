package app

import "testing"

func TestCommercialTestDatabaseURLGuard(t *testing.T) {
	tests := []struct {
		name, url string
		want      bool
	}{
		{"postgres loopback", "postgres://fixture:dummy@127.0.0.1/zerobrand_test", true},
		{"postgresql localhost", "postgresql://fixture:dummy@localhost:5432/zerobrand_test?sslmode=disable&connect_timeout=5", true},
		{"decoded test path", "postgres://localhost/%7Aerobrand_test", true},
		{"empty", "", false},
		{"production database", "postgres://localhost/zerobrand", false},
		{"remote database", "postgres://db.example/zerobrand_test", false},
		{"wrong scheme", "https://localhost/zerobrand_test", false},
		{"missing scheme", "//localhost/zerobrand_test", false},
		{"fragment", "postgres://localhost/zerobrand_test#unexpected", false},
		{"dbname override", "postgres://localhost/zerobrand_test?dbname=zerobrand", false},
		{"database override", "postgres://localhost/zerobrand_test?database=zerobrand", false},
		{"host override", "postgres://localhost/zerobrand_test?host=db.example", false},
		{"hostaddr override", "postgres://localhost/zerobrand_test?hostaddr=203.0.113.1", false},
		{"port override", "postgres://localhost/zerobrand_test?port=6432", false},
		{"service override", "postgres://localhost/zerobrand_test?service=production", false},
		{"servicefile override", "postgres://localhost/zerobrand_test?servicefile=/tmp/example", false},
		{"encoded override key", "postgres://localhost/zerobrand_test?%64bname=zerobrand", false},
		{"case insensitive override", "postgres://localhost/zerobrand_test?DBNAME=zerobrand", false},
		{"empty override", "postgres://localhost/zerobrand_test?dbname=", false},
		{"malformed query escape", "postgres://localhost/zerobrand_test?sslmode=%zz", false},
		{"malformed query separator", "postgres://localhost/zerobrand_test?sslmode=disable;dbname=zerobrand", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeTestDatabaseURL(tt.url); got != tt.want {
				t.Errorf("test database URL guard = %v, want %v", got, tt.want)
			}
		})
	}
}
