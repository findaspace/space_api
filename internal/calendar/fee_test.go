package calendar

import "testing"

func testFees() FeeConfig {
	return FeeConfig{NightlyBps: 1000, TermBps: 1000, TermCapMonths: 1, MinMinor: 2000}
}

func TestNightlyFee(t *testing.T) {
	if got := NightlyFee(testFees(), 60000); got != 6000 {
		t.Errorf("= %d, want 6000", got)
	}
}

// The case that made this phase necessary: GHS 1,300 a month for twelve
// months. The old flat rate billed GHS 1,560 on top of a year's rent.
func TestTermFeeIsCappedAtOneMonth(t *testing.T) {
	if got := TermFee(testFees(), 130000, 12); got != 130000 {
		t.Errorf("twelve months = %d, want the one-month cap of 130000", got)
	}
}

// Under the cap the percentage governs, so a shorter lease costs less.
func TestShorterLeasePaysLess(t *testing.T) {
	six := TermFee(testFees(), 130000, 6)
	twelve := TermFee(testFees(), 130000, 12)

	if six != 78000 {
		t.Errorf("six months = %d, want 78000", six)
	}
	if six >= twelve {
		t.Errorf("six months (%d) should cost less than twelve (%d)", six, twelve)
	}
}

func TestFeeFloor(t *testing.T) {
	// A GHS 50 a month lock-up for one month: ten percent is GHS 5, below the
	// floor, so the floor applies.
	if got := TermFee(testFees(), 5000, 1); got != 2000 {
		t.Errorf("= %d, want the 2000 floor", got)
	}
}

func TestZeroTerm(t *testing.T) {
	if got := TermFee(testFees(), 130000, 0); got != 0 {
		t.Errorf("= %d, want 0", got)
	}
	if got := TermFee(testFees(), 0, 12); got != 0 {
		t.Errorf("= %d, want 0", got)
	}
}

func TestNoCapConfigured(t *testing.T) {
	cfg := testFees()
	cfg.TermCapMonths = 0

	// Without a cap the percentage runs free, which is exactly the behaviour
	// the cap exists to prevent.
	if got := TermFee(cfg, 130000, 12); got != 156000 {
		t.Errorf("= %d, want the uncapped 156000", got)
	}
}
