package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
)

const codeDigits = 6

type Hasher struct {
	pepper []byte
}

func NewHasher(pepper []byte) (*Hasher, error) {
	if len(pepper) < 32 {
		return nil, errors.New("identity: otp pepper must be at least 32 bytes")
	}
	return &Hasher{pepper: pepper}, nil
}

func (h *Hasher) Hash(phone, code string) []byte {
	mac := hmac.New(sha256.New, h.pepper)
	mac.Write([]byte(phone))
	mac.Write([]byte{0})
	mac.Write([]byte(code))
	return mac.Sum(nil)
}

func (h *Hasher) Match(phone, code string, want []byte) bool {
	return subtle.ConstantTimeCompare(h.Hash(phone, code), want) == 1
}

var codeSpace = new(big.Int).Exp(big.NewInt(10), big.NewInt(codeDigits), nil)

func GenerateCode() (string, error) {
	n, err := rand.Int(rand.Reader, codeSpace)
	if err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}
	return fmt.Sprintf("%0*d", codeDigits, n), nil
}
