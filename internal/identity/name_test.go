package identity

import (
	"errors"
	"testing"
)

func TestNormalizeDisplayNameAcceptsRealNames(t *testing.T) {
	for in, want := range map[string]string{
		"Ama Serwaa":            "Ama Serwaa",
		"  Kwame   Mensah  ":    "Kwame Mensah",
		"Nii Armah Ashitey III": "Nii Armah Ashitey III",
		"Adwoa O'Brien-Addo":    "Adwoa O'Brien-Addo",
		"Ɛfua Nyamekye":         "Ɛfua Nyamekye", // Akan letters are letters
		"Jo":                    "Jo",
	} {
		got, err := NormalizeDisplayName(in)
		if err != nil || got != want {
			t.Errorf("NormalizeDisplayName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// The attack: a name is visible before booking, when redaction hides contact
// details in messages, so a name must not carry them either.
func TestNormalizeDisplayNameRefusesContactDetails(t *testing.T) {
	for _, in := range []string{
		"Kofi 0244123456",
		"Kofi 024 412 3456",
		"Kofi +233244123456",
		"kofi@gmail.com",
		"Kofi wa.me/233244",
		"Kofi http://x.gh",
		"Kofi www.kofi.gh",
		"call kofi.com",
	} {
		if _, err := NormalizeDisplayName(in); !errors.Is(err, ErrNameContact) {
			t.Errorf("NormalizeDisplayName(%q) err = %v, want ErrNameContact", in, err)
		}
	}
}

func TestNormalizeDisplayNameShape(t *testing.T) {
	cases := map[string]error{
		"":                       ErrNameLength,
		"   ":                    ErrNameLength,
		"A":                      ErrNameLength,
		"12":                     ErrNameLetters,
		"--":                     ErrNameLetters,
		string(make([]byte, 61)): ErrNameLength,
	}
	for in, want := range cases {
		if _, err := NormalizeDisplayName(in); !errors.Is(err, want) {
			t.Errorf("NormalizeDisplayName(%q) err = %v, want %v", in, err, want)
		}
	}
}
