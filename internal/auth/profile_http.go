package auth

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// ServeProfileHTTP is mounted after migration11 and shares the current schema
// gate. Identity locks are released during bounded image processing, then the
// same live session and schema are checked again before committing.
func (s *Service) ServeProfileHTTP(w http.ResponseWriter, r *http.Request) {
	s.serveProfileHTTP(w, r, normalizeProfileAvatar)
}

// The unexported decoder parameter lets concurrency tests pause the real handler
// between its two authentication transactions. Production always normalizes.
func (s *Service) serveProfileHTTP(w http.ResponseWriter, r *http.Request, decode func(string) ([]byte, error)) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	allowed := ""
	switch r.URL.Path {
	case "/api/profile/me":
		allowed = "GET"
	case "/api/profile/avatar":
		allowed = "GET, POST"
	default:
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != "GET" && !(r.Method == "POST" && r.URL.Path == "/api/profile/avatar") {
		w.Header().Set("Allow", allowed)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	if r.URL.RawQuery != "" {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if r.Method == "POST" && r.Header.Get("Origin") != s.origin {
		write(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	cookie, err := r.Cookie(s.cookieName())
	if err != nil || len(cookie.Value) != 43 {
		write(w, 401, map[string]string{"error": "unauthenticated"})
		return
	}
	var in profileUpload
	if r.Method == "POST" {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, profileMaxBodyBytes))
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		in, err = validateProfileBody(raw)
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	value, avatar, err := s.handleProfile(ctx, r, in, decode)
	// This marker is produced only after authenticating this GET. It also binds
	// an empty avatar response, so cross-tab cookie changes cannot mix identities.
	if owner, ok := value.(profileAvatarOwner); ok && owner > 0 {
		w.Header().Set("X-Profile-User-ID", strconv.FormatInt(int64(owner), 10))
	}
	if err != nil {
		var f failure
		if errors.As(err, &f) {
			if f.status != 503 {
				s.recordFailure(ctx, "profile")
			}
			if f.status == 429 {
				w.Header().Set("Retry-After", "900")
			}
			write(w, f.status, map[string]string{"error": f.code})
		} else {
			s.recordFailure(ctx, "profile")
			write(w, 500, map[string]string{"error": "internal"})
		}
		return
	}
	if avatar != nil {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", `inline; filename="avatar.png"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(avatar)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(avatar)
		return
	}
	write(w, 200, value)
}

type profileAvatarOwner int64

func (s *Service) handleProfile(ctx context.Context, r *http.Request, in profileUpload, decode func(string) ([]byte, error)) (any, []byte, error) {

	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	if err = profileSchemaGate(ctx, tx); err != nil {
		return nil, nil, err
	}
	if r.Method == "POST" {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err := s.rate(ctx, "ip:"+ip, 60); err != nil {
			return nil, nil, err
		}
	}
	actor, _, _, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, nil, err
	}
	var value any
	var avatar []byte
	switch {
	case r.Method == "POST":
		if in.UserID != actor.ID {
			return nil, nil, fail(403, "forbidden")
		}
		if err = s.rate(ctx, "profile:"+strconv.FormatInt(actor.ID, 10), 30); err != nil {
			return nil, nil, err
		}
		// Release tenant/user locks before bounded but CPU-heavy image work.
		// A second transaction below must revalidate the SAME cookie after it.
		if err = tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		png, decodeErr := decode(in.File)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		tx, err = conn.Begin(ctx)
		if err != nil {
			return nil, nil, err
		}
		defer tx.Rollback(ctx)
		if err = profileSchemaGate(ctx, tx); err != nil {
			return nil, nil, err
		}
		current, _, _, authErr := s.authenticateAccess(ctx, tx, r)
		if authErr != nil {
			return nil, nil, authErr
		}
		if current.ID != actor.ID || current.tenant != actor.tenant {
			return nil, nil, fail(403, "forbidden")
		}
		err = s.savePersonalAvatar(ctx, tx, current, png)
		value = map[string]string{"result": "success"}
	case r.URL.Path == "/api/profile/me":
		value, err = readPersonalProfile(ctx, tx, actor)
	default:
		avatar, err = readPersonalAvatar(ctx, tx, actor)
		var missing failure
		if err == nil || (errors.As(err, &missing) && missing.status == 404 && missing.code == "avatar_not_found") {
			value = profileAvatarOwner(actor.ID)
		}
	}
	if err != nil {
		return value, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return value, avatar, nil
}
