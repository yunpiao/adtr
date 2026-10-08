package domainconfig

import (
	"crypto/rand"
	"encoding/binary"
)

const (
	// Version 2 is reserved for operation-account credentials. Domain connection
	// credentials keep their original version 1 envelope and authenticated data.
	operationCredentialVersion = byte(2)
	operationCredentialPurpose = "ADTR/operation-account-credential/v2"
)

// OperationSealedCredential is an authenticated envelope for one operation
// account. It is deliberately distinct from a domain connection credential.
// Treat the whole value as credential material; JSON and formatting redact it.
type OperationSealedCredential struct {
	KeyID      string `json:"-"`
	Ciphertext []byte `json:"-"`
}

func (OperationSealedCredential) String() string   { return "[sealed operation credential]" }
func (OperationSealedCredential) GoString() string { return "[sealed operation credential]" }

// SealOperationCredential binds caller-owned bytes to their purpose, active key,
// tenant, domain, account and credential revision. It retains no plaintext and
// does not alter the input. The caller should clear plaintext after use.
func (v *Vault) SealOperationCredential(tenantID, domainID, accountID string, revision int64, plaintext []byte) (OperationSealedCredential, error) {
	if v == nil || v.aead == nil {
		return OperationSealedCredential{}, ErrUnavailable
	}
	if !validIdentity(tenantID) || !validIdentity(domainID) || !validIdentity(accountID) || revision < 1 || len(plaintext) == 0 || len(plaintext) > MaxCredentialBytes {
		return OperationSealedCredential{}, ErrCredential
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return OperationSealedCredential{}, ErrCredential
	}
	envelope := make([]byte, 1+len(nonce), 1+len(nonce)+len(plaintext)+v.aead.Overhead())
	envelope[0] = operationCredentialVersion
	copy(envelope[1:], nonce)
	envelope = v.aead.Seal(envelope, nonce, plaintext, operationCredentialAAD(v.keyID, tenantID, domainID, accountID, revision))
	return OperationSealedCredential{KeyID: v.keyID, Ciphertext: envelope}, nil
}

// OpenOperationCredential authenticates an account envelope before returning
// caller-owned plaintext. An unconfigured key ID returns ErrKeyUnavailable;
// malformed input, substitution and authentication failures return ErrCredential.
// Errors never contain identities, credential bytes or underlying cipher errors.
func (v *Vault) OpenOperationCredential(tenantID, domainID, accountID string, revision int64, sealed OperationSealedCredential) ([]byte, error) {
	if v == nil || v.aead == nil {
		return nil, ErrUnavailable
	}
	if !validIdentity(tenantID) || !validIdentity(domainID) || !validIdentity(accountID) || revision < 1 || !validToken(sealed.KeyID, 64) {
		return nil, ErrCredential
	}
	if sealed.KeyID != v.keyID {
		return nil, ErrKeyUnavailable
	}
	data := sealed.Ciphertext
	headerSize := 1 + v.aead.NonceSize()
	if len(data) < headerSize+v.aead.Overhead()+1 || len(data) > headerSize+v.aead.Overhead()+MaxCredentialBytes || data[0] != operationCredentialVersion {
		return nil, ErrCredential
	}
	plaintext, err := v.aead.Open(nil, data[1:headerSize], data[headerSize:], operationCredentialAAD(v.keyID, tenantID, domainID, accountID, revision))
	if err != nil {
		clear(plaintext)
		return nil, ErrCredential
	}
	return plaintext, nil
}

func operationCredentialAAD(keyID, tenantID, domainID, accountID string, revision int64) []byte {
	var aad []byte
	for _, value := range []string{operationCredentialPurpose, keyID, tenantID, domainID, accountID} {
		aad = binary.BigEndian.AppendUint32(aad, uint32(len(value)))
		aad = append(aad, value...)
	}
	// All components, including the fixed-width revision, are length-prefixed.
	aad = binary.BigEndian.AppendUint32(aad, 8)
	return binary.BigEndian.AppendUint64(aad, uint64(revision))
}
