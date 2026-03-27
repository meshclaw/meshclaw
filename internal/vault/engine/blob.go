package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	VAULTMagic    = "SV01"
	KeySize       = 32
	NonceSize     = 12
	SaltSize      = 32
	BlobVersion   = 1
	Argon2Time    = 3
	Argon2Memory  = 65536 // KiB (64 MB), matches Python
	Argon2Threads = 4
)

type EncryptedBlob struct {
	Magic      [4]byte
	Version    byte
	Salt       []byte
	Nonce      []byte
	Ciphertext []byte
	CreatedAt  string
	Context    string
}

func (b *EncryptedBlob) ToBytes() []byte {
	ctx := []byte(b.Context)
	ts := []byte(b.CreatedAt)
	buf := make([]byte, 0, 4+1+SaltSize+NonceSize+2+len(ctx)+2+len(ts)+4+len(b.Ciphertext))
	buf = append(buf, b.Magic[:]...)
	buf = append(buf, b.Version)
	buf = append(buf, b.Salt...)
	buf = append(buf, b.Nonce...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(ctx)))
	buf = append(buf, ctx...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(ts)))
	buf = append(buf, ts...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(b.Ciphertext)))
	buf = append(buf, b.Ciphertext...)
	return buf
}

func BlobFromBytes(data []byte) (*EncryptedBlob, error) {
	if len(data) < 4+1+SaltSize+NonceSize+4 {
		return nil, errors.New("truncated vault blob")
	}
	pos := 0
	var magic [4]byte
	copy(magic[:], data[pos:pos+4])
	pos += 4
	if string(magic[:]) != VAULTMagic {
		return nil, fmt.Errorf("invalid magic %q", string(magic[:]))
	}
	ver := data[pos]
	pos++
	if ver != BlobVersion {
		return nil, fmt.Errorf("unsupported version %d", ver)
	}
	salt := append([]byte(nil), data[pos:pos+SaltSize]...)
	pos += SaltSize
	nonce := append([]byte(nil), data[pos:pos+NonceSize]...)
	pos += NonceSize
	if len(data) < pos+4 {
		return nil, errors.New("truncated context")
	}
	ctxLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if len(data) < pos+ctxLen+2 {
		return nil, errors.New("truncated")
	}
	ctx := string(data[pos : pos+ctxLen])
	pos += ctxLen
	tsLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if len(data) < pos+tsLen+4 {
		return nil, errors.New("truncated timestamp")
	}
	ts := string(data[pos : pos+tsLen])
	pos += tsLen
	cipherLen := int(binary.BigEndian.Uint32(data[pos : pos+4]))
	pos += 4
	if len(data) < pos+cipherLen {
		return nil, errors.New("truncated ciphertext")
	}
	ct := append([]byte(nil), data[pos:pos+cipherLen]...)
	return &EncryptedBlob{
		Magic:      magic,
		Version:    ver,
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: ct,
		CreatedAt:  ts,
		Context:    ctx,
	}, nil
}

func NowUTCISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00")
}
