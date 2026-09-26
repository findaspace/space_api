package httpapi

import "github.com/google/uuid"

// bookingVisibility is what one viewer may see of a booking beyond its own
// fields.
type bookingVisibility struct {
	// Guest is the requester's name, membership age and verified phone. The
	// host sees it on every booking, including a request not yet answered,
	// because deciding whether to hand someone a lease without knowing who
	// they are is not a decision.
	Guest bool

	// Address is the exact point and GhanaPost address. The host always sees
	// their own place. The guest sees it only once the booking is paid:
	// before that, an exact address is what lets someone skip the platform and
	// settle with the landlord directly. The listing page promises exactly
	// this, "shared once your booking is confirmed", and this is where the
	// promise is kept.
	Address bool
}

// addressStatuses are the states in which a guest has paid and still needs the
// address. Cancelled and declined are left out: a booking that did not go
// ahead has no need of the front door.
var addressStatuses = map[string]bool{"confirmed": true, "active": true, "completed": true}

func visibilityFor(viewer, hostID, guestID uuid.UUID, status string) bookingVisibility {
	switch viewer {
	case hostID:
		return bookingVisibility{Guest: true, Address: true}
	case guestID:
		return bookingVisibility{Address: addressStatuses[status]}
	default:
		return bookingVisibility{}
	}
}
