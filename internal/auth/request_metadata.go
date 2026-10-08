package auth

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func newAuditContext(r *http.Request) *auditContext {
	a := &auditContext{tenant: "system", requestID: randomToken(16)}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			a.peerIP = ip.String()
		}
	}
	// Business handlers only record requests after matching their static routes.
	// Never capture raw queries, request bodies, headers or caller-supplied IDs.
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") && len(path) <= 128 {
		safe := true
		for _, c := range path {
			if c != '/' && c != '-' && (c < 'a' || c > 'z') {
				safe = false
				break
			}
		}
		if safe {
			a.requestPath = path
		}
	}
	return a
}

func setAuditMetadata(ctx context.Context, tx pgx.Tx, u User) error {
	a, _ := ctx.Value(auditContextKey{}).(*auditContext)
	if a == nil {
		a = &auditContext{}
	}
	_, err := tx.Exec(ctx, `SELECT set_config('adtr.audit_username',$1,true),set_config('adtr.audit_ip',$2,true),set_config('adtr.audit_path',$3,true),set_config('adtr.audit_request_id',$4,true)`, u.Username, a.peerIP, a.requestPath, a.requestID)
	return err
}
