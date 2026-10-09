//go:build integration

// The synthetic LDAP fixture is a separate integration-test executable. It is
// deliberately excluded from the normal application build and has no AD client.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"unicode/utf8"
)

type configuration struct {
	cert, key, startTLSAddress, ldapsAddress string
	controlDir                               string
	directoryMode                            bool
	directoryV2                              bool
	directoryEmpty, directorySlow            bool
	username, password                       []byte
}

func readConfiguration(args []string, getenv func(string) string) (configuration, error) {
	var cfg configuration
	flags := flag.NewFlagSet("adtr-ldap-fixture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.cert, "cert", "", "synthetic server certificate PEM path")
	flags.StringVar(&cfg.key, "key", "", "synthetic server private key PEM path")
	flags.StringVar(&cfg.controlDir, "control-dir", "", "optional disposable integration control directory")
	flags.BoolVar(&cfg.directoryMode, "directory-mode", false, "enable the fixed synthetic directory dictionary for integration tests")
	flags.BoolVar(&cfg.directoryV2, "directory-v2", false, "select the fixed eight-attribute dictionary; requires directory mode")
	flags.BoolVar(&cfg.directoryEmpty, "directory-empty", false, "return zero synthetic objects; requires directory mode")
	flags.BoolVar(&cfg.directorySlow, "directory-slow", false, "return five synthetic pages with a fixed two-second delay each; requires directory mode")
	flags.StringVar(&cfg.startTLSAddress, "starttls-listen", "127.0.0.1:389", "fixture StartTLS IP:389")
	flags.StringVar(&cfg.ldapsAddress, "ldaps-listen", "127.0.0.1:636", "fixture LDAPS IP:636")
	if flags.Parse(args) != nil || flags.NArg() != 0 || cfg.cert == "" || cfg.key == "" ||
		!validListenAddress(cfg.startTLSAddress, "389") || !validListenAddress(cfg.ldapsAddress, "636") ||
		((cfg.directoryV2 || cfg.directoryEmpty || cfg.directorySlow) && !cfg.directoryMode) {
		return configuration{}, errors.New("invalid synthetic LDAP fixture configuration")
	}
	cfg.username = []byte(getenv("ADTR_LDAP_FIXTURE_USERNAME"))
	cfg.password = []byte(getenv("ADTR_LDAP_FIXTURE_PASSWORD"))
	if len(cfg.username) == 0 || len(cfg.username) > 1024 || !utf8.Valid(cfg.username) ||
		len(cfg.password) == 0 || len(cfg.password) > 4096 {
		clear(cfg.username)
		clear(cfg.password)
		return configuration{}, errors.New("invalid synthetic LDAP fixture credentials")
	}
	for _, c := range cfg.username {
		if c == 0 {
			clear(cfg.username)
			clear(cfg.password)
			return configuration{}, errors.New("invalid synthetic LDAP fixture credentials")
		}
	}
	return cfg, nil
}

func validListenAddress(value, wantPort string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port != wantPort {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && !ip.IsMulticast()
}

func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer) error {
	cfg, err := readConfiguration(args, getenv)
	if err != nil {
		return err
	}
	defer clear(cfg.username)
	defer clear(cfg.password)
	control, err := openFixtureControl(cfg.controlDir)
	if err != nil {
		return err
	}
	if control != nil {
		defer control.root.Close()
	}
	certificate, err := tls.LoadX509KeyPair(cfg.cert, cfg.key)
	if err != nil {
		return errors.New("synthetic LDAP fixture certificate unavailable")
	}
	startTLS, err := net.Listen("tcp", cfg.startTLSAddress)
	if err != nil {
		return errors.New("synthetic LDAP fixture StartTLS listener unavailable")
	}
	defer startTLS.Close()
	ldaps, err := net.Listen("tcp", cfg.ldapsAddress)
	if err != nil {
		return errors.New("synthetic LDAP fixture LDAPS listener unavailable")
	}
	defer ldaps.Close()
	fixture := server{
		tlsConfig:      &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}},
		username:       cfg.username,
		password:       cfg.password,
		control:        control,
		directoryMode:  cfg.directoryMode,
		directoryV2:    cfg.directoryV2,
		directoryEmpty: cfg.directoryEmpty,
		directorySlow:  cfg.directorySlow,
	}
	if _, err := fmt.Fprintln(output, "synthetic LDAP fixture ready"); err != nil {
		return errors.New("synthetic LDAP fixture readiness output unavailable")
	}
	return fixture.serve(ctx, []endpoint{{listener: startTLS}, {listener: ldaps, ldaps: true}})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	stop()
	if err != nil {
		// All startup errors are fixed strings; neither credentials nor peer
		// packets, certificate contents, or filesystem error details are logged.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
