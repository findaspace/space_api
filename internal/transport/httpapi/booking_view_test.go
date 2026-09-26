package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

func TestBookingVisibility(t *testing.T) {
	host, guest, stranger := uuid.New(), uuid.New(), uuid.New()

	cases := []struct {
		name   string
		viewer uuid.UUID
		status string
		want   bookingVisibility
	}{
		// The host decides on a request knowing who asked.
		{"host, unanswered request", host, "held", bookingVisibility{Guest: true, Address: true}},
		{"host, confirmed", host, "confirmed", bookingVisibility{Guest: true, Address: true}},

		// The guest gets the address only once they have paid.
		{"guest, request pending", guest, "held", bookingVisibility{}},
		{"guest, approved but unpaid", guest, "approved", bookingVisibility{}},
		{"guest, paid", guest, "confirmed", bookingVisibility{Address: true}},
		{"guest, staying", guest, "active", bookingVisibility{Address: true}},
		{"guest, stayed", guest, "completed", bookingVisibility{Address: true}},
		{"guest, cancelled", guest, "cancelled", bookingVisibility{}},
		{"guest, declined", guest, "declined", bookingVisibility{}},
		{"guest, expired", guest, "expired", bookingVisibility{}},

		// A guest never sees another guest's profile, even their own is not
		// shown back to them as "the guest".
		{"guest never sees a guest profile", guest, "confirmed", bookingVisibility{Address: true}},

		// Anyone else sees nothing extra, whatever the status.
		{"stranger", stranger, "confirmed", bookingVisibility{}},
	}
	for _, c := range cases {
		if got := visibilityFor(c.viewer, host, guest, c.status); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// A host booking their own listing cannot happen (the API refuses it), but if
// the ids ever coincided the host's view must win, never leak less or more.
func TestHostViewWinsWhenIDsCoincide(t *testing.T) {
	same := uuid.New()
	if got := visibilityFor(same, same, same, "held"); got != (bookingVisibility{Guest: true, Address: true}) {
		t.Errorf("got %+v", got)
	}
}
