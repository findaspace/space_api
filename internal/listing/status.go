package listing

import (
	"errors"
	"slices"
)

var (
	ErrNotFound      = errors.New("listing: space not found")
	ErrBadTransition = errors.New("listing: status transition not allowed")
	ErrNotReady      = errors.New("listing: space is not ready to submit")
	ErrConflict      = errors.New("listing: space changed concurrently")
)

// transitions is the complete state machine. Anything not listed is illegal,
// which is why the check is a lookup rather than a chain of conditionals.
var transitions = map[Status][]Status{
	StatusDraft:     {StatusInReview, StatusArchived},
	StatusInReview:  {StatusPublished, StatusDraft, StatusArchived},
	StatusPublished: {StatusDraft, StatusSuspended, StatusArchived},
	StatusSuspended: {StatusPublished, StatusArchived},
	StatusArchived:  {},
}

func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}
