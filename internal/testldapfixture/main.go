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
	emptyTransitionDir                       string
	directoryMode                            bool
	directoryV2                              bool
	directoryV3                              bool
	userExportFailureV3                      bool
	groupExportFailureV3                     bool
	directoryV4                              bool
	groupMembersV4Pagination                 bool
	groupMembersV4Refresh                    bool
	userAssetsV2                             bool
	computerAssetsV3                         bool
	groupAssetsV3                            bool
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
	flags.StringVar(&cfg.emptyTransitionDir, "empty-transition-dir", "", "optional private marker directory; requires exclusive nonempty fast group/computer dictionary3 mode")
	flags.BoolVar(&cfg.directoryMode, "directory-mode", false, "enable the fixed synthetic directory dictionary for integration tests")
	flags.BoolVar(&cfg.directoryV2, "directory-v2", false, "select the fixed eight-attribute dictionary; requires directory mode")
	flags.BoolVar(&cfg.directoryV4, "directory-v4", false, "select the fixed eleven-attribute dictionary; requires exclusive directory mode")
	flags.BoolVar(&cfg.groupMembersV4Refresh, "group-members-v4-refresh", false, "enable private empty transition; requires exclusive nonempty fast dictionary4 mode")
	flags.BoolVar(&cfg.groupMembersV4Pagination, "group-members-v4-pagination", false, "select 26 synthetic groups and four non-groups; requires exclusive nonempty fast dictionary4 mode")
	flags.BoolVar(&cfg.groupExportFailureV3, "group-export-failure-v3", false, "select one XML-invalid synthetic group SAM; requires exclusive group dictionary3 mode")
	flags.BoolVar(&cfg.userExportFailureV3, "user-export-failure-v3", false, "select one XML-invalid synthetic mail; requires exclusive nonempty fast dictionary3 mode")
	flags.BoolVar(&cfg.directoryV3, "directory-v3", false, "select the fixed ten-attribute dictionary; requires directory mode")
	flags.BoolVar(&cfg.computerAssetsV3, "computer-assets-v3", false, "select isolated computer rows; requires dictionary3 nonempty fast mode")
	flags.BoolVar(&cfg.groupAssetsV3, "group-assets-v3", false, "select isolated group rows; requires dictionary3 nonempty fast mode")
	flags.BoolVar(&cfg.userAssetsV2, "user-assets-v2", false, "select isolated user-asset rows; requires dictionary 2 and nonempty fast mode")
	flags.BoolVar(&cfg.directoryEmpty, "directory-empty", false, "return zero synthetic objects; requires directory mode")
	flags.BoolVar(&cfg.directorySlow, "directory-slow", false, "return five synthetic pages with a fixed two-second delay each; requires directory mode")
	flags.StringVar(&cfg.startTLSAddress, "starttls-listen", "127.0.0.1:389", "fixture StartTLS IP:389")
	flags.StringVar(&cfg.ldapsAddress, "ldaps-listen", "127.0.0.1:636", "fixture LDAPS IP:636")
	if flags.Parse(args) != nil || flags.NArg() != 0 || cfg.cert == "" || cfg.key == "" ||
		!validListenAddress(cfg.startTLSAddress, "389") || !validListenAddress(cfg.ldapsAddress, "636") ||
		((cfg.directoryV2 || cfg.directoryV3 || cfg.directoryV4 || cfg.directoryEmpty || cfg.directorySlow) && !cfg.directoryMode) ||
		(cfg.directoryV4 && (cfg.directoryV2 || cfg.directoryV3 || cfg.userAssetsV2 || cfg.computerAssetsV3 || cfg.groupAssetsV3 || (cfg.emptyTransitionDir != "" && !cfg.groupMembersV4Refresh) || cfg.controlDir != "")) ||
		(cfg.groupMembersV4Refresh && (!cfg.directoryMode || !cfg.directoryV4 || cfg.emptyTransitionDir == "" || cfg.directoryEmpty || cfg.directorySlow || cfg.groupMembersV4Pagination)) ||
		(cfg.groupMembersV4Pagination && (!cfg.directoryMode || !cfg.directoryV4 || cfg.directoryEmpty || cfg.directorySlow)) ||
		(cfg.directoryV3 && (cfg.directoryV2 || cfg.userAssetsV2)) ||
		(cfg.groupExportFailureV3 && (!cfg.directoryMode || !cfg.directoryV3 || !cfg.groupAssetsV3 || cfg.userExportFailureV3 || cfg.directoryV2 || cfg.directoryV4 || cfg.userAssetsV2 || cfg.computerAssetsV3 || cfg.groupMembersV4Pagination || cfg.groupMembersV4Refresh || cfg.directoryEmpty || cfg.directorySlow || cfg.controlDir != "" || cfg.emptyTransitionDir != "")) ||
		(cfg.userExportFailureV3 && (!cfg.directoryMode || !cfg.directoryV3 || cfg.directoryV2 || cfg.directoryV4 || cfg.userAssetsV2 || cfg.computerAssetsV3 || cfg.groupAssetsV3 || cfg.groupMembersV4Pagination || cfg.groupMembersV4Refresh || cfg.directoryEmpty || cfg.directorySlow || cfg.controlDir != "" || cfg.emptyTransitionDir != "")) ||
		(cfg.userAssetsV2 && (!cfg.directoryMode || !cfg.directoryV2 || cfg.directoryEmpty || cfg.directorySlow)) ||
		(cfg.computerAssetsV3 && (!cfg.directoryMode || !cfg.directoryV3 || cfg.directoryEmpty || cfg.directorySlow || cfg.userAssetsV2 || cfg.groupAssetsV3)) ||
		(cfg.groupAssetsV3 && (!cfg.directoryMode || !cfg.directoryV3 || cfg.directoryEmpty || cfg.directorySlow || cfg.userAssetsV2 || cfg.computerAssetsV3)) ||
		(cfg.emptyTransitionDir != "" && !cfg.groupMembersV4Refresh && (!cfg.directoryMode || !cfg.directoryV3 || cfg.directoryV2 || cfg.userAssetsV2 ||
			cfg.directoryEmpty || cfg.directorySlow || cfg.computerAssetsV3 == cfg.groupAssetsV3 || cfg.controlDir != "")) {
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
	emptyTransition, err := openFixtureEmptyTransition(cfg.emptyTransitionDir)
	if err != nil {
		return err
	}
	if emptyTransition != nil {
		defer emptyTransition.root.Close()
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
		tlsConfig:                &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}},
		username:                 cfg.username,
		password:                 cfg.password,
		control:                  control,
		emptyTransition:          emptyTransition,
		directoryMode:            cfg.directoryMode,
		directoryV2:              cfg.directoryV2,
		directoryV3:              cfg.directoryV3,
		userExportFailureV3:      cfg.userExportFailureV3,
		groupExportFailureV3:     cfg.groupExportFailureV3,
		directoryV4:              cfg.directoryV4,
		groupMembersV4Pagination: cfg.groupMembersV4Pagination,
		userAssetsV2:             cfg.userAssetsV2,
		computerAssetsV3:         cfg.computerAssetsV3,
		groupAssetsV3:            cfg.groupAssetsV3,
		directoryEmpty:           cfg.directoryEmpty,
		directorySlow:            cfg.directorySlow,
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
