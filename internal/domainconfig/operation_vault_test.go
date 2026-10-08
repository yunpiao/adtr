package domainconfig

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
)

func TestOperationVaultRoundTripNonceAndOwnership(t *testing.T) {
	vault := syntheticVault(t, 0x71)
	plain := []byte(`{"version":1,"username":"SYNTHETIC\\reader","password":" synthetic secret "}`)
	original := bytes.Clone(plain)
	first, err := vault.SealOperationCredential("tenant-a", "domain-a", "account-a", 3, plain)
	if err != nil {
		t.Fatal(err)
	}
	second, err := vault.SealOperationCredential("tenant-a", "domain-a", "account-a", 3, plain)
	if err != nil {
		t.Fatal(err)
	}
	if first.Ciphertext[0] == credentialVersion || bytes.Equal(first.Ciphertext, second.Ciphertext) || bytes.Equal(first.Ciphertext[1:13], second.Ciphertext[1:13]) {
		t.Fatal("operation envelope version or nonce is not distinct")
	}
	if !bytes.Equal(plain, original) || bytes.Contains(first.Ciphertext, []byte("synthetic secret")) {
		t.Fatal("seal altered or exposed plaintext")
	}
	clear(plain)
	opened, err := vault.OpenOperationCredential("tenant-a", "domain-a", "account-a", 3, first)
	if err != nil || !bytes.Equal(opened, original) {
		t.Fatal("round trip or caller ownership failed", err)
	}
	clear(opened)
	again, err := vault.OpenOperationCredential("tenant-a", "domain-a", "account-a", 3, first)
	if err != nil || !bytes.Equal(again, original) {
		t.Fatal("returned plaintext changed envelope", err)
	}
	clear(again)
}

func TestOperationVaultRejectsContextAndEnvelopeTampering(t *testing.T) {
	vault := syntheticVault(t, 0x72)
	sealed, err := vault.SealOperationCredential("ab", "cd", "ef", 7, []byte("synthetic secret"))
	if err != nil {
		t.Fatal(err)
	}
	type testCase struct {
		name, tenant, domain, account string
		revision                      int64
		vault                         *Vault
		sealed                        OperationSealedCredential
	}
	tests := []testCase{
		{"tenant", "other", "cd", "ef", 7, vault, sealed},
		{"domain", "ab", "other", "ef", 7, vault, sealed},
		{"account", "ab", "cd", "other", 7, vault, sealed},
		{"revision", "ab", "cd", "ef", 8, vault, sealed},
		{"tenant/domain concatenation", "a", "bcd", "ef", 7, vault, sealed},
		{"domain/account concatenation", "ab", "c", "def", 7, vault, sealed},
		{"same key ID wrong key", "ab", "cd", "ef", 7, syntheticVault(t, 0x73), sealed},
		{"malformed key ID", "ab", "cd", "ef", 7, vault, OperationSealedCredential{"bad:key", sealed.Ciphertext}},
		{"empty key ID", "ab", "cd", "ef", 7, vault, OperationSealedCredential{"", sealed.Ciphertext}},
		{"oversize key ID", "ab", "cd", "ef", 7, vault, OperationSealedCredential{strings.Repeat("k", 65), sealed.Ciphertext}},
		{"nil ciphertext", "ab", "cd", "ef", 7, vault, OperationSealedCredential{sealed.KeyID, nil}},
		{"oversize ciphertext", "ab", "cd", "ef", 7, vault, OperationSealedCredential{sealed.KeyID, make([]byte, MaxCredentialBytes+30)}},
	}
	otherID, err := newVault("synthetic-key-2", bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatal(err)
	}
	tests = append(tests, testCase{"key ID authenticated", "ab", "cd", "ef", 7, otherID, OperationSealedCredential{otherID.keyID, sealed.Ciphertext}})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plain, err := tc.vault.OpenOperationCredential(tc.tenant, tc.domain, tc.account, tc.revision, tc.sealed)
			assertOperationCredentialFailure(t, plain, err)
		})
	}
	for i := range sealed.Ciphertext {
		t.Run(fmt.Sprintf("corrupt byte %d", i), func(t *testing.T) {
			data := bytes.Clone(sealed.Ciphertext)
			data[i] ^= 1
			plain, err := vault.OpenOperationCredential("ab", "cd", "ef", 7, OperationSealedCredential{sealed.KeyID, data})
			assertOperationCredentialFailure(t, plain, err)
		})
	}
	for length := 0; length < len(sealed.Ciphertext); length++ {
		plain, err := vault.OpenOperationCredential("ab", "cd", "ef", 7, OperationSealedCredential{sealed.KeyID, sealed.Ciphertext[:length]})
		assertOperationCredentialFailure(t, plain, err)
	}
	missing := OperationSealedCredential{KeyID: "synthetic-unavailable-key", Ciphertext: sealed.Ciphertext}
	plain, err := vault.OpenOperationCredential("ab", "cd", "ef", 7, missing)
	if plain != nil || !errors.Is(err, ErrKeyUnavailable) || err.Error() != "domain credential key unavailable" {
		t.Fatal("unavailable key was not safely classified", err)
	}
}

func assertOperationCredentialFailure(t *testing.T, plain []byte, err error) {
	t.Helper()
	if plain != nil || !errors.Is(err, ErrCredential) || err.Error() != "invalid domain credential" {
		t.Fatal("credential accepted or unsafe error returned", err)
	}
}

func TestOperationVaultRejectsDomainCredentialSubstitution(t *testing.T) {
	vault := syntheticVault(t, 0x74)
	domain, err := vault.Seal("tenant", "domain", 1, []byte("synthetic secret"))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := vault.SealOperationCredential("tenant", "domain", "account", 1, []byte("synthetic secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rewriteVersion := range []bool{false, true} {
		asOperation := OperationSealedCredential{domain.KeyID, bytes.Clone(domain.Ciphertext)}
		asDomain := SealedCredential{operation.KeyID, bytes.Clone(operation.Ciphertext)}
		if rewriteVersion {
			// Purpose separation must still reject an attacker rewriting the
			// unauthenticated wire version to the desired envelope type.
			asOperation.Ciphertext[0] = operationCredentialVersion
			asDomain.Ciphertext[0] = credentialVersion
		}
		plain, err := vault.OpenOperationCredential("tenant", "domain", "account", 1, asOperation)
		assertOperationCredentialFailure(t, plain, err)
		plain, err = vault.Open("tenant", "domain", 1, asDomain)
		assertOperationCredentialFailure(t, plain, err)
	}
	plain, err := vault.Open("tenant", "domain", 1, domain)
	if err != nil || string(plain) != "synthetic secret" || domain.Ciphertext[0] != 1 {
		t.Fatal("domain v1 round trip regressed", err)
	}
	clear(plain)
}

func TestOperationVaultPreservesExistingDomainV1Envelope(t *testing.T) {
	// Frozen independently with AES-256-GCM, synthetic key byte 0x74 and nonce
	// 000102030405060708090a0b. The vector uses the original F01 AAD encoding.
	encoded := "01000102030405060708090a0bc34e05e5e907f49a9776b0ef609f43c841f0673c1660196225147d31aaa45786c95c0495fa87d186af29"
	data, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	vault := syntheticVault(t, 0x74)
	sealed := SealedCredential{KeyID: "synthetic-key-1", Ciphertext: data}
	plain, err := vault.Open("tenant", "domain", 1, sealed)
	if err != nil || string(plain) != "synthetic v1 compatibility" {
		t.Fatal("pre-existing F01 v1 envelope no longer opens", err)
	}
	clear(plain)
	plain, err = vault.OpenOperationCredential("tenant", "domain", "account", 1, OperationSealedCredential(sealed))
	assertOperationCredentialFailure(t, plain, err)
}

func TestOperationVaultBoundsAndDisabledValues(t *testing.T) {
	vault := syntheticVault(t, 0x75)
	sealed, err := vault.SealOperationCredential("tenant", "domain", "account", 1, []byte("synthetic secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "bad space", "bad\tcontrol", "bad\x00identity", "bad\u00a0space", string([]byte{0xff}), strings.Repeat("i", 257)} {
		for position := range 3 {
			ids := [3]string{"tenant", "domain", "account"}
			ids[position] = invalid
			if _, err := vault.SealOperationCredential(ids[0], ids[1], ids[2], 1, []byte("p")); !errors.Is(err, ErrCredential) {
				t.Fatal("invalid identity accepted by seal")
			}
			plain, err := vault.OpenOperationCredential(ids[0], ids[1], ids[2], 1, sealed)
			assertOperationCredentialFailure(t, plain, err)
		}
	}
	for _, revision := range []int64{0, -1, math.MinInt64} {
		if _, err := vault.SealOperationCredential("tenant", "domain", "account", revision, []byte("p")); !errors.Is(err, ErrCredential) {
			t.Fatal("invalid revision accepted by seal")
		}
		plain, err := vault.OpenOperationCredential("tenant", "domain", "account", revision, sealed)
		assertOperationCredentialFailure(t, plain, err)
	}
	for _, plain := range [][]byte{nil, {}, make([]byte, MaxCredentialBytes+1)} {
		if _, err := vault.SealOperationCredential("tenant", "domain", "account", 1, plain); !errors.Is(err, ErrCredential) {
			t.Fatal("invalid plaintext length accepted")
		}
	}
	for _, size := range []int{1, MaxCredentialBytes} {
		identity := strings.Repeat("界", 85) + "a"
		original := bytes.Repeat([]byte{'p'}, size)
		sealed, err := vault.SealOperationCredential(identity, identity, identity, math.MaxInt64, original)
		if err != nil {
			t.Fatal("maximum identity or supported plaintext length rejected", err)
		}
		plain, err := vault.OpenOperationCredential(identity, identity, identity, math.MaxInt64, sealed)
		if err != nil || !bytes.Equal(plain, original) {
			t.Fatal("boundary round trip failed", err)
		}
		clear(plain)
	}
	for _, disabled := range []*Vault{nil, {}} {
		if _, err := disabled.SealOperationCredential("tenant", "domain", "account", 1, []byte("p")); !errors.Is(err, ErrUnavailable) {
			t.Fatal("disabled vault sealed")
		}
		if plain, err := disabled.OpenOperationCredential("tenant", "domain", "account", 1, sealed); plain != nil || !errors.Is(err, ErrUnavailable) {
			t.Fatal("disabled vault opened")
		}
	}
}

func TestOperationCredentialJSONAndFormattingRedaction(t *testing.T) {
	sealed := OperationSealedCredential{KeyID: "synthetic-key-must-not-leak", Ciphertext: []byte("synthetic-ciphertext-must-not-leak")}
	for _, object := range []any{sealed, &sealed} {
		encoded, err := json.Marshal(object)
		if err != nil || string(encoded) != "{}" {
			t.Fatal("JSON exposed credential material")
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			if got := fmt.Sprintf(format, object); got != "[sealed operation credential]" {
				t.Fatal("formatted credential was not redacted")
			}
		}
	}
}

func TestConcurrentOperationAndDomainVaultUse(t *testing.T) {
	vault := syntheticVault(t, 0x76)
	var wait sync.WaitGroup
	for i := range 32 {
		wait.Go(func() {
			account := fmt.Sprintf("synthetic-account-%d", i)
			for revision := int64(1); revision <= 25; revision++ {
				sealed, err := vault.SealOperationCredential("tenant", "domain", account, revision, []byte("synthetic secret"))
				if err != nil {
					t.Error(err)
					return
				}
				plain, err := vault.OpenOperationCredential("tenant", "domain", account, revision, sealed)
				if err != nil || string(plain) != "synthetic secret" {
					t.Error("concurrent operation round trip failed", err)
					return
				}
				clear(plain)
				domain, err := vault.Seal("tenant", "domain", revision, []byte("synthetic domain"))
				if err != nil {
					t.Error(err)
					return
				}
				plain, err = vault.Open("tenant", "domain", revision, domain)
				if err != nil || string(plain) != "synthetic domain" {
					t.Error("concurrent domain round trip failed", err)
					return
				}
				clear(plain)
			}
		})
	}
	wait.Wait()
}

func FuzzOpenOperationCredential(f *testing.F) {
	vault, err := newVault("synthetic-key-1", bytes.Repeat([]byte{0x77}, 32))
	if err != nil {
		f.Fatal(err)
	}
	sealed, err := vault.SealOperationCredential("tenant", "domain", "account", 1, []byte("synthetic secret"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(sealed.KeyID, sealed.Ciphertext, "tenant", "domain", "account", int64(1))
	f.Add(sealed.KeyID, []byte{}, "tenant", "domain", "account", int64(1))
	f.Add("unknown-key", []byte{1, 2, 3}, "tenant", "domain", "account", int64(1))
	f.Fuzz(func(t *testing.T, keyID string, data []byte, tenant, domain, account string, revision int64) {
		if len(data) > MaxCredentialBytes+30 || len(keyID) > 65 || len(tenant) > 257 || len(domain) > 257 || len(account) > 257 {
			return
		}
		original := bytes.Clone(data)
		plain, err := vault.OpenOperationCredential(tenant, domain, account, revision, OperationSealedCredential{keyID, data})
		if !bytes.Equal(original, data) {
			t.Fatal("open modified its input")
		}
		if err == nil {
			if len(plain) == 0 || len(plain) > MaxCredentialBytes {
				t.Fatal("open returned plaintext outside bounds")
			}
			clear(plain)
		} else if plain != nil || err != ErrCredential && err != ErrKeyUnavailable {
			t.Fatal("open returned unsafe failure", err)
		}
	})
}
