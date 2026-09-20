package listing

import "testing"

func TestTransitions(t *testing.T) {
	allowed := [][2]Status{
		{StatusDraft, StatusInReview},
		{StatusInReview, StatusPublished},
		{StatusInReview, StatusDraft},
		{StatusPublished, StatusSuspended},
		{StatusSuspended, StatusPublished},
		{StatusPublished, StatusArchived},
	}
	for _, tc := range allowed {
		if !CanTransition(tc[0], tc[1]) {
			t.Errorf("%s to %s should be allowed", tc[0], tc[1])
		}
	}

	denied := [][2]Status{
		{StatusDraft, StatusPublished}, // review cannot be skipped
		{StatusArchived, StatusDraft},  // archive is terminal
		{StatusArchived, StatusPublished},
		{StatusDraft, StatusDraft},
		{StatusSuspended, StatusDraft},
	}
	for _, tc := range denied {
		if CanTransition(tc[0], tc[1]) {
			t.Errorf("%s to %s should be denied", tc[0], tc[1])
		}
	}
}

func TestEveryStatusHasAnEntry(t *testing.T) {
	all := []Status{StatusDraft, StatusInReview, StatusPublished, StatusSuspended, StatusArchived}
	for _, s := range all {
		if _, ok := transitions[s]; !ok {
			t.Errorf("%s missing from the transition table", s)
		}
	}
}
