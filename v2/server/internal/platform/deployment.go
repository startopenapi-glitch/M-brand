package platform

import (
	"errors"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ValidateDatabaseURL runs before opening a connection or applying migrations.
// Never include a connection string in an error: it may contain credentials.
func ValidateDatabaseURL(dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return errors.New("M-brand requires an explicit database URL; no legacy fallback is allowed")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid M-brand database configuration (credentials hidden)")
	}
	if strings.EqualFold(cfg.ConnConfig.Database, "zerobrand") || cfg.ConnConfig.Database == "" {
		return errors.New("M-brand requires its own database; the legacy database is prohibited")
	}
	return nil
}

// ValidateDeploymentConfig is also used by direct binary starts, before any
// secret, database, migration, administrator or worker initialization.
func ValidateDeploymentConfig(identity, dsn, origin string) error {
	if identity != "m-brand" {
		return errors.New("set MBRAND_DEPLOYMENT_ID=m-brand in your independent deployment configuration")
	}
	if err := ValidateDatabaseURL(dsn); err != nil {
		return err
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return errors.New("M-brand requires a valid independent public origin")
	}
	host := strings.ToLower(strings.TrimRight(u.Hostname(), "."))
	if host == "0-brand.com" || strings.HasSuffix(host, ".0-brand.com") {
		return errors.New("M-brand cannot use the original operator's domain")
	}
	return nil
}
