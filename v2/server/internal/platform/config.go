package platform

import (
	"os"
	"strings"
)

// Config is the process-level configuration surface. Domain packages must not
// read environment variables directly.
type Config struct {
	Port         string
	PublicOrigin string
	GenDir       string
}

func LoadConfig() Config {
	port := os.Getenv("ZEROBRAND_PORT")
	if port == "" {
		port = "8830"
	}
	origin := os.Getenv("ZEROBRAND_PUBLIC_ORIGIN")
	if origin == "" {
		origin = "https://localhost:8443"
	}
	return Config{Port: port, PublicOrigin: strings.TrimRight(origin, "/"), GenDir: os.Getenv("ZEROBRAND_GEN_DIR")}
}
