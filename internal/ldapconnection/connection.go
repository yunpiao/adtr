package ldapconnection

import (
	"context"
	"crypto/tls"
	"net"
)

// withBoundConnection owns one connection and its cancellation watcher. It
// always closes and joins before returning or propagating a caller panic. The
// caller chooses a bounded total context; dial and TLS/bind budgets stay fixed.
func withBoundConnection(ctx context.Context, cfg Config, credential Credential, network transport, consume func(context.Context, net.Conn) error) error {
	// DNS and the single dial share a single two-second budget.
	dialCtx, stopDial := context.WithTimeout(ctx, dialTimeout)
	defer stopDial()
	address := cfg.DialIP
	if !address.IsValid() {
		if err := authorize(dialCtx, cfg, StageDNS); err != nil {
			return err
		}
		addresses, err := network.lookup(dialCtx, "ip", cfg.ServerName)
		if err != nil {
			return classifyIO(dialCtx, err, StageDNS)
		}
		if len(addresses) > 16 {
			return failure(CodeEgressDenied, StageDNS)
		}
		for _, candidate := range addresses {
			if !allowedAddress(cfg.AllowedNetworks, candidate) {
				return failure(CodeEgressDenied, StageDNS)
			}
		}
		if len(addresses) > 0 {
			address = addresses[0].Unmap()
		}
		if !address.IsValid() {
			return failure(CodeDNSFailed, StageDNS)
		}
	}
	if !allowedAddress(cfg.AllowedNetworks, address) {
		return failure(CodeEgressDenied, StageDial)
	}
	address = address.Unmap()
	if err := dialCtx.Err(); err != nil {
		return classifyIO(dialCtx, err, StageDial)
	}
	port := "636"
	if cfg.Mode == StartTLS {
		port = "389"
	}
	if err := authorize(dialCtx, cfg, StageDial); err != nil {
		return err
	}
	raw, err := network.dial(dialCtx, "tcp", net.JoinHostPort(address.String(), port))
	if err != nil {
		return classifyIO(dialCtx, err, StageDial)
	}
	stopDial()
	// This is the only goroutine owned by the adapter. Closing the raw connection
	// interrupts TLS and all reads/writes; cleanup joins it before every return.
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = raw.Close()
		case <-stopWatch:
		}
	}()
	defer func() {
		close(stopWatch)
		_ = raw.Close()
		<-watchDone
	}()

	tlsCtx, stopTLS := context.WithTimeout(ctx, stepTimeout)
	defer stopTLS()
	if err := authorize(tlsCtx, cfg, StageTLS); err != nil {
		return err
	}
	if err := setStepDeadline(tlsCtx, raw); err != nil {
		return classifyIO(ctx, err, StageTLS)
	}
	if cfg.Mode == StartTLS {
		if err := startTLS(tlsCtx, raw); err != nil {
			return err
		}
	}
	// StartTLS negotiation and TLS handshake share the same three-second
	// deadline. ServerName stays the configured DNS name when DialIP is pinned.
	secured := tls.Client(raw, &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.ServerName,
		RootCAs:    cfg.Roots,
	})
	if err := secured.HandshakeContext(ctx); err != nil {
		return classifyTLS(ctx, err)
	}
	stopTLS()
	bindCtx, stopBind := context.WithTimeout(ctx, stepTimeout)
	defer stopBind()
	if err := authorize(bindCtx, cfg, StageBind); err != nil {
		return err
	}
	if err := setStepDeadline(bindCtx, secured); err != nil {
		return classifyIO(bindCtx, err, StageBind)
	}
	if err := bind(bindCtx, secured, credential); err != nil {
		return err
	}
	stopBind()
	return consume(ctx, secured)
}
