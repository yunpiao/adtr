package runtime

import (
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Config struct {
	DatabaseURL string
	Database    *pgx.ConnConfig
	ListenAddr  string
}

func LoadConfig(mode string, getenv func(string) string) (Config, error) {
	if mode != "api" && mode != "worker" && mode != "migrate" {
		return Config{}, errors.New("mode must be api, worker or migrate")
	}
	c := Config{DatabaseURL: getenv("ADTR_DATABASE_URL"), ListenAddr: getenv("ADTR_LISTEN_ADDR")}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Path == "" || u.Path == "/" {
		return Config{}, errors.New("ADTR_DATABASE_URL must be a PostgreSQL URL with host and database")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query["sslmode"]) != 1 {
		return Config{}, errors.New("database requires an explicit TLS mode")
	}
	if query.Get("sslmode") != "verify-full" && !(getenv("ADTR_DEVELOPMENT") == "true" && query.Get("sslmode") == "disable") {
		return Config{}, errors.New("database requires sslmode=verify-full; disable is limited to explicit development mode")
	}
	// Reject ambiguous or connection-redirecting URL parameters before parsing.
	// In pgx, ssl=true can override sslmode with require. Reject both orders.
	for key, values := range query {
		if len(values) != 1 || key == "ssl" || key == "host" || key == "hostaddr" || key == "service" || key == "servicefile" {
			return Config{}, errors.New("database URL contains an ambiguous or unsupported connection parameter")
		}
	}
	parsed, err := pgx.ParseConfig(c.DatabaseURL)
	if err != nil {
		return Config{}, errors.New("invalid database configuration")
	}
	if query.Get("sslmode") == "verify-full" {
		if !verifiedEndpoint(parsed.Host, parsed.Port, parsed.TLSConfig) {
			return Config{}, errors.New("database requires verified TCP TLS for every endpoint")
		}
		for _, fallback := range parsed.Fallbacks {
			if fallback == nil || !verifiedEndpoint(fallback.Host, fallback.Port, fallback.TLSConfig) {
				return Config{}, errors.New("database requires verified TCP TLS for every endpoint")
			}
		}
	}
	// Retain the exact validated configuration; connections must not reparse
	// environment variables or service files after validation.
	c.Database = parsed
	// Never return the connection string or parse error: either may contain credentials.
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8080"
		if mode == "worker" {
			c.ListenAddr = "127.0.0.1:8081"
		}
	}
	_, port, err := net.SplitHostPort(c.ListenAddr)
	n, numberErr := strconv.Atoi(port)
	if err != nil || numberErr != nil || n < 1 || n > 65535 {
		return Config{}, errors.New("ADTR_LISTEN_ADDR must contain a valid host and port")
	}
	return c, nil
}

func verifiedEndpoint(host string, port uint16, config *tls.Config) bool {
	network, _ := pgconn.NetworkAddress(host, port)
	return network == "tcp" && host != "" && config != nil && !config.InsecureSkipVerify && config.ServerName == host
}
