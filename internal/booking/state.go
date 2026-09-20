package booking

import "slices"

// Nightly has no approval step: instant book means payment decides. Term does,
// because a landlord vets a tenant before any money moves.
var nightlyTransitions = map[Status][]Status{
	StatusHeld:      {StatusConfirmed, StatusCancelled, StatusExpired},
	StatusConfirmed: {StatusCompleted, StatusCancelled},
	StatusCompleted: {},
	StatusCancelled: {},
	StatusDeclined:  {},
	StatusExpired:   {},
	StatusApproved:  {},
	StatusActive:    {},
}

var termTransitions = map[Status][]Status{
	StatusHeld:      {StatusApproved, StatusDeclined, StatusCancelled, StatusExpired},
	StatusApproved:  {StatusConfirmed, StatusCancelled, StatusExpired},
	StatusConfirmed: {StatusActive, StatusCancelled},
	StatusActive:    {StatusCompleted, StatusCancelled},
	StatusCompleted: {},
	StatusCancelled: {},
	StatusDeclined:  {},
	StatusExpired:   {},
}

func transitionsFor(mode Mode) map[Status][]Status {
	if mode == ModeTerm {
		return termTransitions
	}
	return nightlyTransitions
}

func CanTransition(mode Mode, from, to Status) bool {
	return slices.Contains(transitionsFor(mode)[from], to)
}

// permitted records who may drive each transition. Encoding this next to the
// state machine keeps authorization from drifting into the handlers, where it
// gets checked in some paths and forgotten in others.
var permitted = map[Status][]Actor{
	StatusApproved:  {ActorHost, ActorAdmin},
	StatusDeclined:  {ActorHost, ActorAdmin},
	StatusConfirmed: {ActorSystem, ActorAdmin},
	StatusActive:    {ActorSystem, ActorAdmin},
	StatusCompleted: {ActorSystem, ActorAdmin},
	StatusExpired:   {ActorSystem},
	StatusCancelled: {ActorGuest, ActorHost, ActorSystem, ActorAdmin},
}

func ActorMay(actor Actor, to Status) bool {
	return slices.Contains(permitted[to], actor)
}

// Authorize is the single gate every status change goes through.
func Authorize(mode Mode, from, to Status, actor Actor) error {
	if !CanTransition(mode, from, to) {
		return ErrBadTransition
	}
	if !ActorMay(actor, to) {
		return ErrNotPermitted
	}
	return nil
}

// Live reports whether a booking still holds its dates. It must agree exactly
// with the constraint predicate in migration 00008.
func Live(s Status) bool {
	switch s {
	case StatusHeld, StatusApproved, StatusConfirmed, StatusActive:
		return true
	}
	return false
}
