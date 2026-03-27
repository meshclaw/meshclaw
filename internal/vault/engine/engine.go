package engine

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/argon2"
)

type VaultEngine struct{}

func (VaultEngine) DeriveKey(password []byte, salt []byte) []byte {
	return argon2.IDKey(password, salt, Argon2Time, Argon2Memory, Argon2Threads, KeySize)
}

func (e VaultEngine) Encrypt(data []byte, password []byte, context string) (*EncryptedBlob, error) {
	return e.EncryptAAD(data, password, context, nil)
}

// EncryptAAD matches Python VaultEngine.encrypt(..., aad=).
func (e VaultEngine) EncryptAAD(data []byte, password []byte, context string, aad []byte) (*EncryptedBlob, error) {
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	key := e.DeriveKey(password, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, data, aad)
	var m [4]byte
	copy(m[:], VAULTMagic)
	return &EncryptedBlob{
		Magic:      m,
		Version:    BlobVersion,
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: ciphertext,
		CreatedAt:  NowUTCISO(),
		Context:    context,
	}, nil
}

func (e VaultEngine) Decrypt(blob *EncryptedBlob, password []byte) ([]byte, error) {
	return e.DecryptAAD(blob, password, nil)
}

func (e VaultEngine) DecryptAAD(blob *EncryptedBlob, password []byte, aad []byte) ([]byte, error) {
	key := e.DeriveKey(password, blob.Salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, blob.Nonce, blob.Ciphertext, aad)
	if err != nil {
		return nil, errors.New("wrong password or corrupted file (GCM auth tag mismatch)")
	}
	return plain, nil
}

func (e VaultEngine) EncryptWithKey(data, key []byte, context string) (*EncryptedBlob, error) {
	if len(key) != KeySize {
		return nil, errors.New("key must be 32 bytes")
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, data, nil)
	var m [4]byte
	copy(m[:], VAULTMagic)
	zsalt := make([]byte, SaltSize) // Python uses zero salt when using raw key
	return &EncryptedBlob{
		Magic:      m,
		Version:    BlobVersion,
		Salt:       zsalt,
		Nonce:      nonce,
		Ciphertext: ciphertext,
		CreatedAt:  NowUTCISO(),
		Context:    context,
	}, nil
}

func (e VaultEngine) DecryptWithKey(blob *EncryptedBlob, key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, errors.New("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, blob.Nonce, blob.Ciphertext, nil)
}

func Sha256Prefix16(b []byte) string {
	h := sha256.Sum256(b)
	return fmtHex(h[:8])
}

func fmtHex(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0xf]
	}
	return string(out)
}
