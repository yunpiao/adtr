package runtime

import (
	"errors"
	"net"
	"net/url"
	"strconv"
)

type Config struct {
	DatabaseURL string
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
