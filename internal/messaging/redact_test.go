package messaging

import (
	"strings"
	"testing"
)

func TestRedactRemovesContactDetails(t *testing.T) {
	cases := []string{
		"call me on 0244123456",
		"my number is +233244123456",
		"reach me: +233 24 412 3456",
		"024-412-3456 anytime",
		"0244.123.456 is my line",
		"233 24 412 3456",
		"email sam@example.com for details",
		"message me wa.me/233244123456",
		"join https://chat.whatsapp.com/AbCdEf",
	}
	for _, in := range cases {
		out, changed := Redact(in)
		if !changed {
			t.Errorf("Redact(%q) left it unchanged", in)
		}
		if strings.ContainsAny(out, "@") || strings.Contains(out, "412") {
			t.Errorf("Redact(%q) = %q still leaks contact details", in, out)
		}
	}
}

// Redaction that eats ordinary sentences makes the product unusable, so the
// false-positive cases matter as much as the true ones.
func TestRedactLeavesOrdinaryTextAlone(t *testing.T) {
	cases := []string{
		"the room is 25 square metres",
		"check in after 14:00, check out by 11:00",
		"GHS 4500 per month, 12 months advance",
		"near the A&C Mall on the Boundary Road",
		"we have borehole water and an inverter",
		"booking SPC-S53KAWW3 confirmed",
	}
	for _, in := range cases {
		out, changed := Redact(in)
		if changed {
			t.Errorf("Redact(%q) = %q; ordinary text was altered", in, out)
		}
	}
}

func TestWhatsAppRedactedWhole(t *testing.T) {
	out, _ := Redact("wa.me/233244123456")
	if strings.Contains(out, "wa.me") {
		t.Errorf("= %q; the link prefix survived", out)
	}
}
