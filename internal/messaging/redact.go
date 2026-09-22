package messaging

import "regexp"

const redactedMarker = "[contact details removed]"

var (
	// Ghanaian mobile numbers with or without +233 or the leading 0, allowing
	// the separators people actually type: spaces, dashes and dots.
	phonePattern = regexp.MustCompile(`(?:\+?233|0)[\s.\-]?[2-5]\d(?:[\s.\-]?\d){7}`)

	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

	// WhatsApp links move the conversation off-platform in one tap.
	whatsappPattern = regexp.MustCompile(`(?i)\b(?:wa\.me|chat\.whatsapp\.com|api\.whatsapp\.com)\S*`)
)

// Redact removes contact details from a pre-booking message and reports
// whether anything was removed.
//
// This is friction, not a wall. "Zero two four one two..." in words passes
// straight through, and no regex will ever catch every encoding. The goal is to
// make leaving the platform deliberate rather than the path of least resistance.
func Redact(body string) (string, bool) {
	out := body
	for _, p := range []*regexp.Regexp{whatsappPattern, emailPattern, phonePattern} {
		out = p.ReplaceAllString(out, redactedMarker)
	}
	return out, out != body
}
