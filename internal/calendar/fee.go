package calendar

// FeeConfig is the platform's cut. Three numbers, because one percentage
// cannot serve both a three-night stay and a twelve-month lease.
type FeeConfig struct {
	NightlyBps    int
	TermBps       int
	TermCapMonths int
	MinMinor      int64
}

// NightlyFee is a straight percentage of what the guest pays for the stay.
// No cap: a nightly booking is short and the percentage stays proportionate.
func NightlyFee(cfg FeeConfig, chargeableMinor int64) int64 {
	if chargeableMinor <= 0 {
		return 0
	}
	return floorFee(cfg, applyBps(chargeableMinor, cfg.NightlyBps))
}

// TermFee is a percentage of the whole lease, capped at a fixed number of
// months' rent.
//
// Uncapped, ten percent of a twelve-month lease is more than a month's rent,
// which is several times what a Ghanaian agent charges. Percentage first so a
// six-month lease pays less than a twelve-month one; cap second so a long
// lease pays the market rate; floor third so a tiny lease is not free.
//
// It depends on rent and term only, never on the advance: a landlord demanding
// twelve months up front must not multiply the platform's cut.
func TermFee(cfg FeeConfig, monthlyRentMinor int64, months int) int64 {
	if months < 1 || monthlyRentMinor <= 0 {
		return 0
	}

	fee := applyBps(monthlyRentMinor*int64(months), cfg.TermBps)

	if cfg.TermCapMonths > 0 {
		capped := monthlyRentMinor * int64(cfg.TermCapMonths)
		if fee > capped {
			fee = capped
		}
	}
	return floorFee(cfg, fee)
}

func floorFee(cfg FeeConfig, fee int64) int64 {
	if fee < cfg.MinMinor {
		return cfg.MinMinor
	}
	return fee
}
