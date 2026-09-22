package review

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func stay(status string, endedDaysAgo int) Stay {
	return Stay{
		BookingID: uuid.New(), SpaceID: uuid.New(),
		GuestID: uuid.New(), HostID: uuid.New(),
		Status: status,
		EndsOn: time.Now().Add(-time.Duration(endedDaysAgo) * 24 * time.Hour),
	}
}

func TestEligible(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		s    Stay
		want error
	}{
		{"completed yesterday", stay("completed", 1), nil},
		{"completed 13 days ago", stay("completed", 13), nil},
		{"window closed", stay("completed", 15), ErrWindowClosed},
		{"still confirmed", stay("confirmed", 1), ErrNotEligible},
		{"cancelled", stay("cancelled", 1), ErrNotEligible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Eligible(tc.s, now); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAuthorComesFromTheBooking(t *testing.T) {
	s := stay("completed", 1)

	role, subject, err := Author(s, s.GuestID)
	if err != nil || role != RoleGuest || subject != s.HostID {
		t.Errorf("guest: role=%s subject=%s err=%v", role, subject, err)
	}

	role, subject, err = Author(s, s.HostID)
	if err != nil || role != RoleHost || subject != s.GuestID {
		t.Errorf("host: role=%s subject=%s err=%v", role, subject, err)
	}

	if _, _, err := Author(s, uuid.New()); !errors.Is(err, ErrNotParty) {
		t.Errorf("stranger: err = %v, want ErrNotParty", err)
	}
}

func TestValidRating(t *testing.T) {
	for _, r := range []int{1, 3, 5} {
		if !ValidRating(r) {
			t.Errorf("%d should be valid", r)
		}
	}
	for _, r := range []int{0, 6, -1} {
		if ValidRating(r) {
			t.Errorf("%d should be invalid", r)
		}
	}
}
