package domainconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"unicode"
	"unicode/utf8"
)

const (
	MaxCredentialBytes  = 64 << 10
	MaxFingerprintBytes = 1 << 20
	credentialVersion   = byte(1)
)

// SealedCredential is a versioned authenticated envelope suitable for BYTEA.
// Treat it as credential material: neither JSON nor formatting includes it.
type SealedCredential struct {
	KeyID      string `json:"-"`
	Ciphertext []byte `json:"-"`
}

func (SealedCredential) String() string   { return "[sealed domain credential]" }
func (SealedCredential) GoString() string { return "[sealed domain credential]" }

// Vault encrypts the complete caller-owned credential pair without parsing it.
// It retains no plaintext, exposes no key and is safe for concurrent use.
type Vault struct {
	keyID          string
	aead           cipher.AEAD
	fingerprintKey [sha256.Size]byte
}

func (Vault) String() string   { return "[domain credential vault]" }
func (Vault) GoString() string { return "[domain credential vault]" }

func newVault(keyID string, key []byte) (*Vault, error) {
	if !validToken(keyID, 64) || len(key) != 32 {
		return nil, ErrConfiguration
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrConfiguration
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrConfiguration
	}
	vault := &Vault{keyID: keyID, aead: aead}
	derive := hmac.New(sha256.New, key)
	derive.Write([]byte("ADTR/domain-fingerprint/subkey/v1"))
	copy(vault.fingerprintKey[:], derive.Sum(nil))
	return vault, nil
}

// Seal binds the bytes to the tenant, domain, credential revision and key ID.
// The caller retains ownership of plaintext and should clear it after use.
func (v *Vault) Seal(tenantID, domainID string, revision int64, plaintext []byte) (SealedCredential, error) {
	if v == nil || v.aead == nil {
		return SealedCredential{}, ErrUnavailable
	}
	if !validIdentity(tenantID) || !validIdentity(domainID) || revision < 1 || len(plaintext) == 0 || len(plaintext) > MaxCredentialBytes {
		return SealedCredential{}, ErrCredential
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return SealedCredential{}, ErrCredential
	}
	envelope := make([]byte, 1+len(nonce), 1+len(nonce)+len(plaintext)+v.aead.Overhead())
	envelope[0] = credentialVersion
	copy(envelope[1:], nonce)
	envelope = v.aead.Seal(envelope, nonce, plaintext, credentialAAD(v.keyID, tenantID, domainID, revision))
	return SealedCredential{KeyID: v.keyID, Ciphertext: envelope}, nil
}

// Open authenticates before returning caller-owned plaintext. An unconfigured
// key ID returns ErrKeyUnavailable; authentication/context failures return
// ErrCredential. Neither error includes identities or raw cipher errors.
func (v *Vault) Open(tenantID, domainID string, revision int64, sealed SealedCredential) ([]byte, error) {
	if v == nil || v.aead == nil {
		return nil, ErrUnavailable
	}
	if !validIdentity(tenantID) || !validIdentity(domainID) || revision < 1 || !validToken(sealed.KeyID, 64) {
		return nil, ErrCredential
	}
	if sealed.KeyID != v.keyID {
		return nil, ErrKeyUnavailable
	}
	data := sealed.Ciphertext
	headerSize := 1 + v.aead.NonceSize()
	if len(data) < headerSize+v.aead.Overhead()+1 || len(data) > headerSize+v.aead.Overhead()+MaxCredentialBytes || data[0] != credentialVersion {
		return nil, ErrCredential
	}
	plaintext, err := v.aead.Open(nil, data[1:headerSize], data[headerSize:], credentialAAD(v.keyID, tenantID, domainID, revision))
	if err != nil {
		clear(plaintext)
		return nil, ErrCredential
	}
	return plaintext, nil
}

// Fingerprint produces an opaque keyed digest for idempotency. canonical may
// include credentials; the caller must never persist a plain hash of that input.
func (v *Vault) Fingerprint(purpose, tenantID string, canonical []byte) (string, error) {
	if v == nil || v.aead == nil {
		return "", ErrUnavailable
	}
	if !validToken(purpose, 64) || !validIdentity(tenantID) || len(canonical) == 0 || len(canonical) > MaxFingerprintBytes {
		return "", ErrCredential
	}
	mac := hmac.New(sha256.New, v.fingerprintKey[:])
	mac.Write([]byte("ADTR/domain-fingerprint/value/v1"))
	for _, value := range [][]byte{[]byte(v.keyID), []byte(purpose), []byte(tenantID), canonical} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		mac.Write(size[:])
		mac.Write(value)
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func credentialAAD(keyID, tenantID, domainID string, revision int64) []byte {
	aad := []byte("ADTR/domain-credential/v1")
	for _, value := range []string{keyID, tenantID, domainID} {
		aad = binary.BigEndian.AppendUint32(aad, uint32(len(value)))
		aad = append(aad, value...)
	}
	return binary.BigEndian.AppendUint64(aad, uint64(revision))
}

func validIdentity(value string) bool {
	if len(value) < 1 || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
