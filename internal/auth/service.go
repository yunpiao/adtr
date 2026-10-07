package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type auditContextKey struct{}
type auditContext struct {
	actor  *int64
	tenant string
	target *int64
}

type Service struct {
	database  *pgx.ConnConfig
	key       []byte
	origin    string
	secure    bool
	dummyHash string
	now       func() time.Time
}
type User struct {
	ID                                          int64     `json:"ID"`
	Username                                    string    `json:"username"`
	Role                                        string    `json:"role"`
	Priv                                        int       `json:"priv"`
	Mobile                                      string    `json:"mobile"`
	Email                                       string    `json:"email"`
	Remark                                      string    `json:"remark"`
	PassStrength                                string    `json:"passStrength"`
	HasMFA                                      bool      `json:"hasMfa"`
	NeedChange                                  bool      `json:"needChangePwd"`
	IsExpired                                   bool      `json:"isExpired"`
	PasswordDays                                int       `json:"passwordNotUpdatedDays"`
	PasswordUpdated                             time.Time `json:"pwdUpdateTm"`
	CSRF                                        string    `json:"csrfToken"`
	tenant, passwordHash, mfaSecret, mfaPending string
	pendingUntil                                *time.Time
	lastStep                                    int64
}
type request struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	TOTPCode    string `json:"totpCode"`
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
	Secret      string `json:"secret"`
	MFACode     string `json:"mfaCode"`
}
type failure struct {
	status int
	code   string
}

func (f failure) Error() string          { return f.code }
func fail(status int, code string) error { return failure{status, code} }

const columns = "u.id,u.tenant_id,u.username,u.password_hash,u.pass_strength,u.role,u.mobile,u.email,u.remark,u.must_change,u.password_updated_at,u.mfa_secret,u.mfa_pending,u.mfa_pending_until,u.mfa_last_step"

func New(database *pgx.ConnConfig, key []byte, origin string, development bool) (*Service, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || !(u.Scheme == "https" || development && u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, errors.New("authentication requires an explicit trusted origin")
	}
	if database == nil || len(key) != 32 {
		return nil, errors.New("authentication requires database configuration and a 32-byte key")
	}
	dummy, err := hashPassword(randomToken(32))
	if err != nil {
		return nil, errors.New("authentication initialization failed")
	}
	return &Service{database: database.Copy(), key: append([]byte(nil), key...), origin: origin, secure: u.Scheme == "https", dummyHash: dummy, now: time.Now}, nil
}
func (s *Service) cookieName() string {
	if s.secure {
		return "__Host-adtr_session"
	}
	return "adtr_session"
}
func (s *Service) cookie(w http.ResponseWriter, token string) {
	c := &http.Cookie{Name: s.cookieName(), Value: token, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600}
	if token == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Service) readUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.tenant, &u.Username, &u.passwordHash, &u.PassStrength, &u.Role, &u.Mobile, &u.Email, &u.Remark, &u.NeedChange, &u.PasswordUpdated, &u.mfaSecret, &u.mfaPending, &u.pendingUntil, &u.lastStep)
	if err != nil {
		return u, err
	}
	u.PasswordUpdated = u.PasswordUpdated.UTC()
	u.HasMFA = u.mfaSecret != ""
	u.PasswordDays = int(s.now().Sub(u.PasswordUpdated).Hours() / 24)
	if u.PasswordDays < 0 {
		u.PasswordDays = 0
	}
	u.IsExpired = u.PasswordDays >= 90
	u.NeedChange = u.NeedChange || u.IsExpired
	if u.Role == "platform_admin" {
		u.Priv = 1
	}
	return u, nil
}
func (s *Service) audit(ctx context.Context, tx pgx.Tx, u User, action string, target int64) error {
	_, err := tx.Exec(ctx, "INSERT INTO adtr.auth_audit(actor_id,tenant_id,action,target_id) VALUES($1,$2,$3,$4)", u.ID, u.tenant, action, target)
	return err
}
func (s *Service) rotate(ctx context.Context, tx pgx.Tx, u *User) (string, error) {
	token := randomToken(32)
	_, err := tx.Exec(ctx, "INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", digest(token), u.ID, s.now().Add(8*time.Hour))
	u.CSRF = csrfToken(s.key, token)
	return token, err
}
func (s *Service) mfa(u *User, code string) bool {
	secret, err := unseal(s.key, u.mfaSecret, strconv.FormatInt(u.ID, 10))
	if err != nil {
		return false
	}
	step, ok := verifyTOTP(secret, code, s.now(), u.lastStep)
	if ok {
		u.lastStep = step
	}
	return ok
}
func (s *Service) rate(ctx context.Context, bucket string, limit int) error {
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	// Expired entries contain no credentials and have no purpose after this window.
	_, err = conn.Exec(ctx, "DELETE FROM adtr.auth_attempts WHERE window_start < $1", s.now().Add(-15*time.Minute))
	if err != nil {
		return err
	}
	var count int
	err = conn.QueryRow(ctx, `INSERT INTO adtr.auth_attempts(bucket,count,window_start) VALUES($1,1,$2)
 ON CONFLICT(bucket) DO UPDATE SET count=adtr.auth_attempts.count+1 RETURNING count`, digest(bucket), s.now()).Scan(&count)
	if err != nil {
		return err
	}
	if count > limit {
		return fail(429, "rate_limited")
	}
	return nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, &auditContext{tenant: "system"})
	r = r.WithContext(ctx)
	path := strings.TrimPrefix(r.URL.Path, "/api/auth")
	expected := map[string]string{"/login": "POST", "/logout": "POST", "/me": "GET", "/password": "POST", "/reset-password": "POST", "/mfa": "GET", "/mfa/enroll": "POST", "/mfa/confirm": "POST", "/mfa/disable": "POST"}
	method, exists := expected[path]
	if !exists {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	var in request
	if r.Method == "POST" {
		if r.Header.Get("Origin") != s.origin {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if readErr != nil || !utf8.Valid(raw) {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		if err = d.Decode(&in); err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		if err = d.Decode(&struct{}{}); err != io.EOF {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
	}
	value, token, clear, err := s.handle(ctx, r, path, in)
	if err != nil {
		s.recordFailure(ctx, path)
		var f failure
		if errors.As(err, &f) {
			if f.status == 429 {
				w.Header().Set("Retry-After", "900")
			}
			write(w, f.status, map[string]string{"error": f.code})
		} else {
			write(w, 500, map[string]string{"error": "internal"})
		}
		return
	}
	if clear {
		s.cookie(w, "")
	} else if token != "" {
		s.cookie(w, token)
	}
	write(w, 200, value)
}
func (s *Service) handle(ctx context.Context, r *http.Request, path string, in request) (any, string, bool, error) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr) // Never trust unconfigured proxy forwarding headers.
	if r.Method == "POST" && path != "/logout" {
		if err := s.rate(ctx, "ip:"+ip, 60); err != nil {
			return nil, "", false, err
		}
	}
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, "", false, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, "", false, err
	}
	defer tx.Rollback(ctx)
	if path == "/login" {
		username, e := normalizeUsername(in.Username)
		if e != nil || len(in.Password) > 256 {
			return nil, "", false, fail(400, "invalid_input")
		}
		if err = s.rate(ctx, "login:"+username, 10); err != nil {
			return nil, "", false, err
		}
		var tenant string
		e = tx.QueryRow(ctx, "SELECT tenant_id FROM adtr.users WHERE username=$1 AND NOT disabled", username).Scan(&tenant)
		if errors.Is(e, pgx.ErrNoRows) {
			checkPassword(s.dummyHash, in.Password)
			return nil, "", false, fail(401, "invalid_credentials")
		}
		if e != nil {
			return nil, "", false, e
		}
		if e = lockIdentityTenant(ctx, tx, tenant); e != nil {
			return nil, "", false, e
		}
		u, e := s.readUser(tx.QueryRow(ctx, "SELECT "+columns+" FROM adtr.users u WHERE username=$1 AND tenant_id=$2 AND NOT disabled FOR UPDATE", username, tenant))
		if errors.Is(e, pgx.ErrNoRows) {
			checkPassword(s.dummyHash, in.Password)
			return nil, "", false, fail(401, "invalid_credentials")
		}
		if e != nil {
			return nil, "", false, e
		}
		if !checkPassword(u.passwordHash, in.Password) {
			return nil, "", false, fail(401, "invalid_credentials")
		}
		if u.HasMFA {
			if in.TOTPCode == "" {
				return nil, "", false, fail(401, "mfa_required")
			}
			if !s.mfa(&u, in.TOTPCode) {
				return nil, "", false, fail(401, "invalid_credentials")
			}
			if _, err = tx.Exec(ctx, "UPDATE adtr.users SET mfa_last_step=$1 WHERE id=$2", u.lastStep, u.ID); err != nil {
				return nil, "", false, err
			}
		}
		token, e := s.rotate(ctx, tx, &u)
		if e != nil {
			return nil, "", false, e
		}
		if e = s.audit(ctx, tx, u, "login", u.ID); e != nil {
			return nil, "", false, e
		}
		if _, err = tx.Exec(ctx, "DELETE FROM adtr.auth_attempts WHERE bucket=$1", digest("login:"+username)); err != nil {
			return nil, "", false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, "", false, err
		}
		return u, token, false, nil
	}
	cookie, e := r.Cookie(s.cookieName())
	if e != nil || len(cookie.Value) != 43 {
		return nil, "", false, fail(401, "unauthenticated")
	}
	var tenant string
	e = tx.QueryRow(ctx, "SELECT u.tenant_id FROM adtr.sessions s JOIN adtr.users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>$2 AND NOT u.disabled", digest(cookie.Value), s.now()).Scan(&tenant)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, "", false, fail(401, "unauthenticated")
	}
	if e != nil {
		return nil, "", false, e
	}
	if e = lockIdentityTenant(ctx, tx, tenant); e != nil {
		return nil, "", false, e
	}
	u, e := s.readUser(tx.QueryRow(ctx, "SELECT "+columns+" FROM adtr.sessions s JOIN adtr.users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>$2 AND NOT u.disabled AND u.tenant_id=$3 FOR UPDATE OF u", digest(cookie.Value), s.now(), tenant))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, "", false, fail(401, "unauthenticated")
	}
	if e != nil {
		return nil, "", false, e
	}
	// Re-read the session after acquiring the user lock so a concurrent password
	// change or MFA rotation cannot authenticate a deleted pre-lock session.
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.sessions WHERE token_hash=$1 AND user_id=$2 AND expires_at>$3)", digest(cookie.Value), u.ID, s.now()).Scan(&active); err != nil {
		return nil, "", false, err
	}
	if !active {
		return nil, "", false, fail(401, "unauthenticated")
	}
	if audit, ok := ctx.Value(auditContextKey{}).(*auditContext); ok {
		audit.actor = &u.ID
		audit.tenant = u.tenant
	}
	u.CSRF = csrfToken(s.key, cookie.Value)
	if r.Method == "POST" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(u.CSRF)) != 1 {
		return nil, "", false, fail(403, "forbidden")
	}
	if path == "/me" {
		return u, "", false, tx.Commit(ctx)
	}
	if path == "/logout" {
		if _, err = tx.Exec(ctx, "DELETE FROM adtr.sessions WHERE token_hash=$1", digest(cookie.Value)); err != nil {
			return nil, "", false, err
		}
		if err = s.audit(ctx, tx, u, "logout", u.ID); err != nil {
			return nil, "", false, err
		}
		return map[string]string{"result": "SUCCESS"}, "", true, tx.Commit(ctx)
	}
	if u.NeedChange && path != "/password" {
		return nil, "", false, fail(403, "password_change_required")
	}
	if path == "/mfa" {
		return map[string]bool{"hasMfa": u.HasMFA}, "", false, tx.Commit(ctx)
	}
	if err = s.rate(ctx, "sensitive:"+strconv.FormatInt(u.ID, 10), 10); err != nil {
		return nil, "", false, err
	}
	var action string
	switch path {
	case "/password":
		if in.Username != "" && strings.ToLower(in.Username) != u.Username {
			return nil, "", false, fail(403, "forbidden")
		}
		if !validPassword(in.NewPassword) || in.NewPassword == in.OldPassword {
			return nil, "", false, fail(400, "invalid_input")
		}
		if !checkPassword(u.passwordHash, in.OldPassword) {
			return nil, "", false, fail(401, "invalid_credentials")
		}
		hash, e := hashPassword(in.NewPassword)
		if e != nil {
			return nil, "", false, e
		}
		if _, err = tx.Exec(ctx, "UPDATE adtr.users SET password_hash=$1,must_change=false,password_updated_at=$2,pass_strength=$4 WHERE id=$3", hash, s.now(), u.ID, passwordStrength(in.NewPassword)); err != nil {
			return nil, "", false, err
		}
		u.PassStrength = passwordStrength(in.NewPassword)
		u.NeedChange = false
		u.IsExpired = false
		u.PasswordDays = 0
		u.PasswordUpdated = s.now().UTC()
		action = "password_change"
	case "/reset-password":
		if u.Role != "platform_admin" || !u.HasMFA {
			return nil, "", false, fail(403, "forbidden")
		}
		if !checkPassword(u.passwordHash, in.Password) || !s.mfa(&u, in.TOTPCode) {
			return nil, "", false, fail(401, "invalid_credentials")
		}
		username, e := normalizeUsername(in.Username)
		if e != nil || !validPassword(in.NewPassword) || username == u.Username {
			return nil, "", false, fail(400, "invalid_input")
		}
		var target int64
		if e = tx.QueryRow(ctx, "SELECT id FROM adtr.users WHERE tenant_id=$1 AND username=$2 FOR UPDATE", u.tenant, username).Scan(&target); errors.Is(e, pgx.ErrNoRows) {
			return nil, "", false, fail(403, "forbidden")
		}
		if e != nil {
			return nil, "", false, e
		}
		hash, e := hashPassword(in.NewPassword)
		if e != nil {
			return nil, "", false, e
		}
		if _, err = tx.Exec(ctx, "UPDATE adtr.users SET password_hash=$1,must_change=true,password_updated_at=$2,pass_strength=$4 WHERE id=$3", hash, s.now(), target, passwordStrength(in.NewPassword)); err != nil {
			return nil, "", false, err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM adtr.sessions WHERE user_id=$1", target); err != nil {
			return nil, "", false, err
		}
		if _, err = tx.Exec(ctx, "UPDATE adtr.users SET mfa_last_step=$1 WHERE id=$2", u.lastStep, u.ID); err != nil {
			return nil, "", false, err
		}
		if err = s.audit(ctx, tx, u, "password_reset", target); err != nil {
			return nil, "", false, err
		}
		return map[string]string{"result": "SUCCESS"}, "", false, tx.Commit(ctx)
	case "/mfa/enroll":
		if u.HasMFA {
			return nil, "", false, fail(400, "invalid_input")
		}
		if !checkPassword(u.passwordHash, in.Password) {
			return nil, "", false, fail(401, "invalid_credentials")
		}
		raw := make([]byte, 20)
		if _, err = rand.Read(raw); err != nil {
			return nil, "", false, err
		}
		secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
		encrypted, e := seal(s.key, secret, strconv.FormatInt(u.ID, 10))
		if e != nil {
			return nil, "", false, e
		}
		if _, err = tx.Exec(ctx, "UPDATE adtr.users SET mfa_pending=$1,mfa_pending_until=$2 WHERE id=$3", encrypted, s.now().Add(10*time.Minute), u.ID); err != nil {
			return nil, "", false, err
		}
		if err = s.audit(ctx, tx, u, "mfa_enroll_begin", u.ID); err != nil {
			return nil, "", false, err
		}
		uri := "otpauth://totp/" + url.PathEscape("ADTR:"+u.Username) + "?secret=" + secret + "&issuer=ADTR&algorithm=SHA1&digits=6&period=30"
		return map[string]string{"secret": secret, "otpauthUri": uri}, "", false, tx.Commit(ctx)
	case "/mfa/confirm":
		if u.HasMFA || u.pendingUntil == nil || !u.pendingUntil.After(s.now()) || !checkPassword(u.passwordHash, in.Password) {
			return nil, "", false, fail(401, "invalid_credentials")
		}
		secret, e := unseal(s.key, u.mfaPending, strconv.FormatInt(u.ID, 10))
		if e != nil {
			return nil, "", false, e
		}
		if subtle.ConstantTimeCompare([]byte(secret), []byte(in.Secret)) != 1 {
			return nil, "", false, fail(400, "invalid_input")
		}
		step, ok := verifyTOTP(secret, in.MFACode, s.now(), -1)
		if !ok {
			return nil, "", false, fail(401, "invalid_credentials")
		}
		if _, err = tx.Exec(ctx, "UPDATE adtr.users SET mfa_secret=mfa_pending,mfa_pending='',mfa_pending_until=NULL,mfa_last_step=$1 WHERE id=$2", step, u.ID); err != nil {
			return nil, "", false, err
		}
		u.HasMFA = true
		action = "mfa_enable"
	case "/mfa/disable":
		if !u.HasMFA || !checkPassword(u.passwordHash, in.Password) || !s.mfa(&u, in.MFACode) {
			return nil, "", false, fail(401, "invalid_credentials")
		}
		if _, err = tx.Exec(ctx, "UPDATE adtr.users SET mfa_secret='',mfa_pending='',mfa_pending_until=NULL,mfa_last_step=-1 WHERE id=$1", u.ID); err != nil {
			return nil, "", false, err
		}
		u.HasMFA = false
		action = "mfa_disable"
	default:
		return nil, "", false, fail(404, "not_found")
	}
	if _, err = tx.Exec(ctx, "DELETE FROM adtr.sessions WHERE user_id=$1", u.ID); err != nil {
		return nil, "", false, err
	}
	token, e := s.rotate(ctx, tx, &u)
	if e != nil {
		return nil, "", false, e
	}
	if err = s.audit(ctx, tx, u, action, u.ID); err != nil {
		return nil, "", false, err
	}
	return u, token, false, tx.Commit(ctx)
}

// Bootstrap creates the first local platform administrator only. There is no
// unauthenticated HTTP signup endpoint and no default password.
func (s *Service) Bootstrap(ctx context.Context, username, password string) error {
	username, err := normalizeUsername(username)
	if err != nil || !validPassword(password) {
		return errors.New("invalid bootstrap input")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return errors.New("bootstrap database unavailable")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(734192802)"); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM adtr.users").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("bootstrap refused: accounts already exist")
	}
	var id int64
	if err = tx.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,pass_strength) VALUES('default',$1,$2,'platform_admin',$3) RETURNING id", username, hash, passwordStrength(password)).Scan(&id); err != nil {
		return errors.New("bootstrap failed")
	}
	if err = s.audit(ctx, tx, User{ID: id, tenant: "default"}, "bootstrap", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) recordFailure(ctx context.Context, path string) {
	if s.database == nil {
		return
	}
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return
	}
	defer conn.Close(ctx)
	audit, _ := ctx.Value(auditContextKey{}).(*auditContext)
	if audit == nil {
		audit = &auditContext{tenant: "system"}
	}
	_, _ = conn.Exec(ctx, "INSERT INTO adtr.auth_audit(actor_id,tenant_id,action,target_id) VALUES($1,$2,$3,$4)", audit.actor, audit.tenant, strings.TrimPrefix(path, "/")+"_denied", audit.target)
}

// All identity and authorization operations acquire tenant then user locks.
// Sharing this order with access mutations avoids cross-admin reset deadlocks.
func lockIdentityTenant(ctx context.Context, tx pgx.Tx, tenant string) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))", tenant)
	return err
}
