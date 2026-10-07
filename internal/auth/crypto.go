package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const passwordIterations = 600000

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("system randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func validPassword(s string) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= 12 && n <= 64 && len(s) <= 256
}
func normalizeUsername(s string) (string, error) {
	if len(s) < 1 || len(s) > 64 {
		return "", errors.New("invalid username")
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return "", errors.New("invalid username")
		}
	}
	return strings.ToLower(s), nil
}
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
func checkPassword(encoded, password string) bool {
	p := strings.Split(encoded, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(p[1])
	if err != nil || iterations != passwordIterations {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(p[2])
	want, e2 := base64.RawStdEncoding.DecodeString(p[3])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(want) != 32 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}
func seal(key []byte, value, aad string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(value), []byte(aad))), nil
}
func unseal(key []byte, value, aad string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(data) < g.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte(aad))
	return string(plain), err
}
func totp(secret string, step int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) < 20 {
		return "", errors.New("invalid TOTP secret")
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(b[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 15
	n := binary.BigEndian.Uint32(h[o:o+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1000000), nil
}
func verifyTOTP(secret, code string, now time.Time, last int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	for _, delta := range []int64{0, -1, 1} {
		step := now.Unix()/30 + delta
		if step <= last {
			continue
		}
		want, err := totp(secret, step)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
func csrfToken(key []byte, token string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("csrf:" + token))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// passwordStrength is display-only; acceptance is determined by validPassword.
func passwordStrength(password string) string {
	var lower, upper, digit, other bool
	for _, r := range password {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
	}
	classes := 0
	for _, present := range []bool{lower, upper, digit, other} {
		if present {
			classes++
		}
	}
	if utf8.RuneCountInString(password) >= 16 && classes >= 3 {
		return "high"
	}
	if classes >= 2 {
		return "middle"
	}
	return "low"
}
