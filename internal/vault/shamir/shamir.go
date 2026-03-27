// Package shamir implements Shamir secret sharing over GF(2^8) with polynomial
// 0x11D (same as Python vault/keymanager.py ShamirSecret).
package shamir

import (
	"crypto/rand"
	"errors"
	"io"
)

var (
	ErrInvalidParams  = errors.New("invalid n/k: need 2 <= k <= n <= 255")
	ErrTooFewShares   = errors.New("need at least 2 shares")
	ErrLengthMismatch = errors.New("share length mismatch")
	ErrDuplicateX     = errors.New("duplicate x coordinate")
)

type Share struct {
	X     byte
	Bytes []byte
}

func (s Share) ToPair() (int, []byte) {
	cp := make([]byte, len(s.Bytes))
	copy(cp, s.Bytes)
	return int(s.X), cp
}

func ShareFromPair(idx int, data []byte) Share {
	cp := make([]byte, len(data))
	copy(cp, data)
	return Share{X: byte(idx), Bytes: cp}
}

// Split splits secret into n shares (k required to recover).
func Split(secret []byte, n, k int) ([]Share, error) {
	if k < 2 || k > n || n > 255 {
		return nil, ErrInvalidParams
	}
	initTables()
	shares := make([]Share, n)
	for i := range shares {
		shares[i].X = byte(i + 1)
		shares[i].Bytes = make([]byte, len(secret))
	}
	for bi, b := range secret {
		coeffs := make([]byte, k)
		coeffs[0] = b
		for j := 1; j < k; j++ {
			var c [1]byte
			if _, err := io.ReadFull(rand.Reader, c[:]); err != nil {
				return nil, err
			}
			coeffs[j] = c[0]
		}
		for si := range shares {
			x := byte(si + 1)
			shares[si].Bytes[bi] = evalPoly(coeffs, x)
		}
	}
	return shares, nil
}

// Recover reconstructs the secret from shares (at least k).
func Recover(shares []Share) ([]byte, error) {
	if len(shares) < 2 {
		return nil, ErrTooFewShares
	}
	initTables()
	length := len(shares[0].Bytes)
	for _, s := range shares {
		if len(s.Bytes) != length {
			return nil, ErrLengthMismatch
		}
	}
	result := make([]byte, length)
	for byteIdx := 0; byteIdx < length; byteIdx++ {
		var value byte
		for i := range shares {
			xi := shares[i].X
			yi := shares[i].Bytes[byteIdx]
			basis := byte(1)
			for j := range shares {
				if i == j {
					continue
				}
				xj := shares[j].X
				denom := xj ^ xi
				if denom == 0 {
					return nil, ErrDuplicateX
				}
				basis = gfMul(basis, gfMul(xj, gfInv(denom)))
			}
			value ^= gfMul(yi, basis)
		}
		result[byteIdx] = value
	}
	return result, nil
}

var expTable [512]byte
var logTable [256]byte
var tablesReady bool

func initTables() {
	if tablesReady {
		return
	}
	var x uint16 = 1
	for i := 0; i < 255; i++ {
		expTable[i] = byte(x)
		logTable[byte(x)] = byte(i)
		x <<= 1
		if x >= 256 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		expTable[i] = expTable[i-255]
	}
	tablesReady = true
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	initTables()
	return expTable[int(logTable[a])+int(logTable[b])]
}

func gfInv(a byte) byte {
	if a == 0 {
		panic("gfInv(0)")
	}
	initTables()
	return expTable[255-int(logTable[a])]
}

func evalPoly(coeffs []byte, x byte) byte {
	var result byte
	for i := len(coeffs) - 1; i >= 0; i-- {
		result = gfMul(result, x) ^ coeffs[i]
	}
	return result
}
