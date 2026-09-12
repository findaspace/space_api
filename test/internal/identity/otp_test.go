package identity

import (
	"strings"
	"testing"
)

func TestGenerateCodeShape(t *testing.T) {
	for i := 0; i < 200; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		if len(code) != codeDigits {
			t.Fatalf("code %q has length %d, want %d", code, len(code), codeDigits)
		}
		if strings.TrimLeft(code, "0123456789") != "" {
			t.Fatalf("code %q is not all digits", code)
		}
	}
}

func TestHasherBindsCodeToPhone(t *testing.T) {
	h, err := NewHasher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}

	want := h.Hash("+233244123456", "123456")

	if !h.Match("+233244123456", "123456", want) {
		t.Error("correct phone and code did not match")
	}
	if h.Match("+233244999999", "123456", want) {
		t.Error("same code matched a different number")
	}
	if h.Match("+233244123456", "123457", want) {
		t.Error("wrong code matched")
	}
	if h.Match("+2332441234", "56", want) {
		t.Error("split boundary collision")
	}
}

func TestNewHasherRejectsShortPepper(t *testing.T) {
	if _, err := NewHasher(make([]byte, 31)); err == nil {
		t.Error("expected error for 31-byte pepper")
	}
}
