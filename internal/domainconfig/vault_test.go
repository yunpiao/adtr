package domainconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func syntheticVault(t *testing.T, keyByte byte) *Vault {
	t.Helper()
	vault, err := newVault("synthetic-key-1", bytes.Repeat([]byte{keyByte}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return vault
}

func TestVaultRoundTripFreshNoncesAndCallerOwnership(t *testing.T) {
	vault := syntheticVault(t, 0x51)
	plain := []byte(`{"username":"synthetic@example.test","password":"synthetic-password"}`)
	first, err := vault.Seal("tenant-a", "domain-a", 1, plain)
	if err != nil {
		t.Fatal(err)
	}
	second, err := vault.Seal("tenant-a", "domain-a", 1, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) || bytes.Equal(first.Ciphertext[1:13], second.Ciphertext[1:13]) {
		t.Fatal("seals reused an envelope or nonce")
	}
	if bytes.Contains(first.Ciphertext, plain) || bytes.Contains(first.Ciphertext, []byte("synthetic-password")) {
		t.Fatal("envelope contains plaintext")
	}
	opened, err := vault.Open("tenant-a", "domain-a", 1, first)
	if err != nil || !bytes.Equal(opened, plain) {
		t.Fatal("round trip failed", err)
	}
	opened[0] ^= 1
	again, err := vault.Open("tenant-a", "domain-a", 1, first)
	if err != nil || !bytes.Equal(again, plain) {
		t.Fatal("returned bytes changed the vault or envelope", err)
	}
	clear(opened)
	clear(again)
}

func TestVaultRejectsContextAndEnvelopeTampering(t *testing.T) {
	vault := syntheticVault(t, 0x32)
	sealed, err := vault.Seal("ab", "c", 7, []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, tenant, domain string
		revision             int64
		vault                *Vault
		sealed               SealedCredential
	}{
		{"wrong tenant", "ac", "c", 7, vault, sealed},
		{"wrong domain", "ab", "d", 7, vault, sealed},
		{"ambiguous concatenation", "a", "bc", 7, vault, sealed},
		{"wrong revision", "ab", "c", 8, vault, sealed},
		{"zero revision", "ab", "c", 0, vault, sealed},
		{"negative revision", "ab", "c", -1, vault, sealed},
		{"wrong key", "ab", "c", 7, syntheticVault(t, 0x33), sealed},
		{"malformed key ID", "ab", "c", 7, vault, SealedCredential{"invalid:key:id", sealed.Ciphertext}},
		{"empty envelope", "ab", "c", 7, vault, SealedCredential{sealed.KeyID, nil}},
		{"oversize envelope", "ab", "c", 7, vault, SealedCredential{sealed.KeyID, make([]byte, MaxCredentialBytes+30)}},
	}
	for _, index := range []int{0, 1, 12, 13, len(sealed.Ciphertext) - 1} {
		corrupt := append([]byte(nil), sealed.Ciphertext...)
		corrupt[index] ^= 1
		tests = append(tests, struct {
			name, tenant, domain string
			revision             int64
			vault                *Vault
			sealed               SealedCredential
		}{fmt.Sprintf("corrupt byte %d", index), "ab", "c", 7, vault, SealedCredential{sealed.KeyID, corrupt}})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plain, err := tc.vault.Open(tc.tenant, tc.domain, tc.revision, tc.sealed)
			if !errors.Is(err, ErrCredential) || plain != nil || err.Error() != "invalid domain credential" {
				t.Fatal("tampered envelope/context accepted or unsafe error", err)
			}
		})
	}
	missingKey := SealedCredential{KeyID: "historical-key-id", Ciphertext: sealed.Ciphertext}
	if plain, err := vault.Open("ab", "c", 7, missingKey); plain != nil || !errors.Is(err, ErrKeyUnavailable) || err.Error() != "domain credential key unavailable" {
		t.Fatal("historical key mismatch not safely classified", err)
	}
}

func TestVaultBoundsNilAndZeroValues(t *testing.T) {
	vault := syntheticVault(t, 0x42)
	for _, tc := range []struct {
		tenant, domain string
		revision       int64
		plain          []byte
	}{
		{"", "domain", 1, []byte("p")},
		{"tenant", "", 1, []byte("p")},
		{strings.Repeat("t", 257), "domain", 1, []byte("p")},
		{"tenant", "domain\n", 1, []byte("p")},
		{"tenant", "domain", 0, []byte("p")},
		{"tenant", "domain", 1, nil},
		{"tenant", "domain", 1, make([]byte, MaxCredentialBytes+1)},
	} {
		if _, err := vault.Seal(tc.tenant, tc.domain, tc.revision, tc.plain); !errors.Is(err, ErrCredential) {
			t.Fatal("invalid seal input accepted")
		}
	}
	maxPlain := bytes.Repeat([]byte{'p'}, MaxCredentialBytes)
	sealed, err := vault.Seal(strings.Repeat("t", 256), strings.Repeat("d", 256), 1<<62, maxPlain)
	if err != nil {
		t.Fatal("maximum valid input rejected", err)
	}
	opened, err := vault.Open(strings.Repeat("t", 256), strings.Repeat("d", 256), 1<<62, sealed)
	if err != nil || !bytes.Equal(opened, maxPlain) {
		t.Fatal("maximum input round trip failed", err)
	}
	for _, empty := range []*Vault{nil, {}} {
		if _, err := empty.Seal("t", "d", 1, []byte("p")); !errors.Is(err, ErrUnavailable) {
			t.Fatal("disabled vault sealed")
		}
		if _, err := empty.Open("t", "d", 1, sealed); !errors.Is(err, ErrUnavailable) {
			t.Fatal("disabled vault opened")
		}
		if _, err := empty.Fingerprint("save", "t", []byte("p")); !errors.Is(err, ErrUnavailable) {
			t.Fatal("disabled vault fingerprinted")
		}
	}
}

func TestFingerprintDeterminismAndContextKeySeparation(t *testing.T) {
	vault := syntheticVault(t, 0x78)
	canonical := []byte(`{"username":"synthetic-user","password":"synthetic-secret"}`)
	want, err := vault.Fingerprint("domain.create", "tenant-a", canonical)
	if err != nil || len(want) != 64 {
		t.Fatal("fingerprint failed", err)
	}
	again, err := vault.Fingerprint("domain.create", "tenant-a", canonical)
	if err != nil || want != again {
		t.Fatal("fingerprint is not deterministic", err)
	}
	for _, tc := range []struct {
		vault           *Vault
		purpose, tenant string
		canonical       []byte
	}{
		{vault, "domain.update", "tenant-a", canonical},
		{vault, "domain.create", "tenant-b", canonical},
		{vault, "domain.create", "tenant-a", append(append([]byte(nil), canonical...), ' ')},
		{syntheticVault(t, 0x79), "domain.create", "tenant-a", canonical},
	} {
		got, err := tc.vault.Fingerprint(tc.purpose, tc.tenant, tc.canonical)
		if err != nil || got == want {
			t.Fatal("fingerprint context/key did not separate", err)
		}
	}
	a, _ := vault.Fingerprint("ab", "c", []byte("d"))
	b, _ := vault.Fingerprint("a", "bc", []byte("d"))
	if a == b {
		t.Fatal("fingerprint context was ambiguous")
	}
	for _, tc := range []struct {
		purpose, tenant string
		canonical       []byte
	}{
		{"", "t", canonical},
		{strings.Repeat("p", 65), "t", canonical},
		{"domain/create", "t", canonical},
		{"create", "", canonical},
		{"create", "t", nil},
		{"create", "t", make([]byte, MaxFingerprintBytes+1)},
	} {
		if _, err := vault.Fingerprint(tc.purpose, tc.tenant, tc.canonical); !errors.Is(err, ErrCredential) {
			t.Fatal("invalid fingerprint input accepted")
		}
	}
}

func TestVaultFormattingAndJSONAreRedacted(t *testing.T) {
	vault := syntheticVault(t, 'X')
	sealed, err := vault.Seal("tenant-secret", "domain-secret", 1, []byte("synthetic-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []any{vault, *vault, sealed, &sealed, &Runtime{vault: vault}} {
		encoded, err := json.Marshal(object)
		if err != nil || string(encoded) != "{}" {
			t.Fatal("JSON exposed vault state")
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			value := fmt.Sprintf(format, object)
			for _, secret := range []string{"synthetic-secret", "tenant-secret", "domain-secret", "synthetic-key-1", strings.Repeat("X", 32)} {
				if strings.Contains(value, secret) {
					t.Fatal("formatted state exposed credential material")
				}
			}
		}
	}
}

func TestConcurrentVaultUse(t *testing.T) {
	vault := syntheticVault(t, 0x61)
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Go(func() {
			for revision := int64(1); revision <= 25; revision++ {
				sealed, err := vault.Seal("t", "d", revision, []byte("synthetic secret"))
				if err != nil {
					t.Error(err)
					return
				}
				plain, err := vault.Open("t", "d", revision, sealed)
				if err != nil || string(plain) != "synthetic secret" {
					t.Error("concurrent round trip failed", err)
					return
				}
				clear(plain)
				if _, err := vault.Fingerprint("create", "t", []byte("synthetic secret")); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wait.Wait()
}
