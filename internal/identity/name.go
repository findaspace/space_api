package identity

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrNameLength  = errors.New("identity: name must be 2 to 60 characters")
	ErrNameLetters = errors.New("identity: name must contain letters")
	ErrNameContact = errors.New("identity: name must not contain contact details")
)

// NormalizeDisplayName checks a name before anyone else sees it.
//
// A display name is shown to the other party before any booking exists, which
// is exactly when message redaction hides phone numbers and links. A name is
// therefore the obvious way around it: "Kofi 0244123456" would put the number
// on every request the host sees. So a name may not carry a phone number, an
// email address or a link, the same things redaction strips from messages.
//
// Seven digits is the threshold because a Ghanaian number without its leading
// zero and prefix is still nine, and nothing a real name needs comes close:
// "Kwame Mensah Jr. II" has none.
func NormalizeDisplayName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")

	n := utf8.RuneCountInString(name)
	if n < 2 || n > 60 {
		return "", ErrNameLength
	}

	letters, digits := 0, 0
	for _, r := range name {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsDigit(r):
			digits++
		}
	}
	if letters == 0 {
		return "", ErrNameLetters
	}

	lower := strings.ToLower(name)
	if digits >= 7 || strings.Contains(lower, "@") ||
		strings.Contains(lower, "http") || strings.Contains(lower, "www.") ||
		strings.Contains(lower, "wa.me") || strings.Contains(lower, ".com") {
		return "", ErrNameContact
	}
	return name, nil
}
