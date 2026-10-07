package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashAndLimits(t *testing.T) {
	hash, err := hashPassword("Correct Password 123")
	if err != nil {
		t.Fatal(err)
	}
	if !checkPassword(hash, "Correct Password 123") || checkPassword(hash, "Wrong Password 123") {
		t.Fatal("password verification failed")
	}
	if strings.Contains(hash, "Correct") {
		t.Fatal("plaintext persisted")
	}
	for _, v := range []string{"", strings.Repeat("x", 11), strings.Repeat("x", 65), string([]byte{0xff})} {
		if validPassword(v) {
			t.Fatalf("invalid password accepted: length %d", len(v))
		}
	}
	for _, v := range []string{strings.Repeat("x", 12), strings.Repeat("界", 64)} {
		if !validPassword(v) {
			t.Fatal("valid boundary rejected")
		}
	}
	for _, v := range []string{"", "root$bad", "pbkdf2-sha256$999999999$x$x"} {
		if checkPassword(v, "test") {
			t.Fatal("invalid hash accepted")
		}
	}
}
func TestUsernames(t *testing.T) {
	for _, s := range []string{"", "a b", "../", "用户", strings.Repeat("a", 65)} {
		if _, e := normalizeUsername(s); e == nil {
			t.Fatal("invalid username accepted")
		}
	}
	if s, e := normalizeUsername("Admin.Name-1"); e != nil || s != "admin.name-1" {
		t.Fatal(s, e)
	}
}
func TestTOTPStandardVectorAndReplay(t *testing.T) {
	// RFC 6238 SHA-1 vector at t=59, truncated to the specified 6 digits.
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	code, err := totp(secret, 1)
	if err != nil || code != "287082" {
		t.Fatal(code, err)
	}
	now := time.Unix(59, 0)
	step, ok := verifyTOTP(secret, code, now, -1)
	if !ok || step != 1 {
		t.Fatal("valid code rejected")
	}
	if _, ok := verifyTOTP(secret, code, now, step); ok {
		t.Fatal("replayed code accepted")
	}
	for _, bad := range []string{"", "28708", "2870820", "abcdef", " 28708"} {
		if _, ok := verifyTOTP(secret, bad, now, -1); ok {
			t.Fatal("invalid code accepted")
		}
	}
	if _, ok := verifyTOTP(secret, code, now.Add(2*time.Minute), -1); ok {
		t.Fatal("expired code accepted")
	}
}
func TestEncryptionBindsUserAndRejectsTampering(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	cipher, err := seal(key, "SYNTHETIC-SECRET", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cipher, "SYNTHETIC") {
		t.Fatal("plaintext secret")
	}
	plain, err := unseal(key, cipher, "user-1")
	if err != nil || plain != "SYNTHETIC-SECRET" {
		t.Fatal(err)
	}
	if _, err = unseal(key, cipher, "user-2"); err == nil {
		t.Fatal("cross-account ciphertext accepted")
	}
	if _, err = unseal(key, "bad", "user-1"); err == nil {
		t.Fatal("bad ciphertext accepted")
	}
	if csrfToken(key, "one") == csrfToken(key, "two") {
		t.Fatal("CSRF not session bound")
	}
}
