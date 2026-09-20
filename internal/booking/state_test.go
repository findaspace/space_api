package booking

import (
	"strings"
	"testing"
)

func TestNightlySkipsApproval(t *testing.T) {
	if CanTransition(ModeNightly, StatusHeld, StatusApproved) {
		t.Error("nightly must not have an approval step")
	}
	if !CanTransition(ModeNightly, StatusHeld, StatusConfirmed) {
		t.Error("nightly held must reach confirmed")
	}
}

func TestTermRequiresApproval(t *testing.T) {
	if CanTransition(ModeTerm, StatusHeld, StatusConfirmed) {
		t.Error("term must not confirm without approval")
	}
	if !CanTransition(ModeTerm, StatusHeld, StatusApproved) {
		t.Error("term held must reach approved")
	}
	if !CanTransition(ModeTerm, StatusApproved, StatusConfirmed) {
		t.Error("term approved must reach confirmed")
	}
}

func TestTerminalStatesAreTerminal(t *testing.T) {
	for _, mode := range []Mode{ModeNightly, ModeTerm} {
		for _, s := range []Status{StatusCompleted, StatusCancelled, StatusDeclined, StatusExpired} {
			if len(transitionsFor(mode)[s]) != 0 {
				t.Errorf("%s/%s should be terminal", mode, s)
			}
		}
	}
}

// A guest must not be able to approve their own application, and a host must
// not be able to mark a booking paid.
func TestActorPermissions(t *testing.T) {
	cases := []struct {
		actor Actor
		to    Status
		want  bool
	}{
		{ActorGuest, StatusApproved, false},
		{ActorGuest, StatusConfirmed, false},
		{ActorGuest, StatusCancelled, true},
		{ActorHost, StatusApproved, true},
		{ActorHost, StatusConfirmed, false},
		{ActorHost, StatusDeclined, true},
		{ActorSystem, StatusConfirmed, true},
		{ActorSystem, StatusExpired, true},
		{ActorHost, StatusExpired, false},
	}
	for _, tc := range cases {
		if got := ActorMay(tc.actor, tc.to); got != tc.want {
			t.Errorf("ActorMay(%s, %s) = %v, want %v", tc.actor, tc.to, got, tc.want)
		}
	}
}

func TestAuthorizeReportsTheRightFailure(t *testing.T) {
	// Legal transition, wrong actor.
	if err := Authorize(ModeTerm, StatusHeld, StatusApproved, ActorGuest); err != ErrNotPermitted {
		t.Errorf("err = %v, want ErrNotPermitted", err)
	}
	// Right actor, illegal transition.
	if err := Authorize(ModeNightly, StatusCompleted, StatusConfirmed, ActorSystem); err != ErrBadTransition {
		t.Errorf("err = %v, want ErrBadTransition", err)
	}
	if err := Authorize(ModeTerm, StatusHeld, StatusApproved, ActorHost); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLiveMatchesConstraintPredicate(t *testing.T) {
	// If this list changes, migration 00008's EXCLUDE predicate must change
	// with it, or the database and the code disagree about what blocks dates.
	live := []Status{StatusHeld, StatusApproved, StatusConfirmed, StatusActive}
	dead := []Status{StatusCompleted, StatusCancelled, StatusDeclined, StatusExpired}

	for _, s := range live {
		if !Live(s) {
			t.Errorf("%s should be live", s)
		}
	}
	for _, s := range dead {
		if Live(s) {
			t.Errorf("%s should not hold dates", s)
		}
	}
}

func TestReferenceShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		ref, err := NewReference()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(ref, "SPC-") || len(ref) != 12 {
			t.Fatalf("reference %q has the wrong shape", ref)
		}
		if strings.ContainsAny(ref[4:], "ILOU") {
			t.Fatalf("reference %q contains an ambiguous character", ref)
		}
		if seen[ref] {
			t.Fatalf("duplicate reference %q after %d draws", ref, i)
		}
		seen[ref] = true
	}
}
