package listing

import "testing"

func fullApartment() map[string]any {
	return map[string]any{
		"bedrooms": int64(2), "bathrooms": int64(2), "self_contained": true,
		"furnishing": "semi_furnished", "kitchen": "private",
		"floor_area_sqm": 74.0, "water_source": "borehole", "power_backup": "inverter",
	}
}

func TestEmptyListingScoresZero(t *testing.T) {
	score, gaps := Score(Inputs{Type: TypeApartment})
	if score != 0 {
		t.Errorf("score = %d, want 0", score)
	}
	if len(gaps) == 0 {
		t.Error("an empty listing should report gaps")
	}
}

func TestFullListingScoresFull(t *testing.T) {
	score, gaps := Score(Inputs{
		Type:        TypeApartment,
		Attributes:  fullApartment(),
		Description: "Quiet gated compound off the Madina road with borehole water and an inverter that runs the lights all night.",
		PhotoCount:  6,
		HasVideo:    true,
	})
	if score != 100 {
		t.Errorf("score = %d, want 100", score)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %v, want none", gaps)
	}
}

// The minimum a listing can publish with still scores above zero, because a
// real listing with one photo is worth showing.
func TestMinimumViableListingScores(t *testing.T) {
	score, _ := Score(Inputs{Type: TypeApartment, PhotoCount: 1})
	if score <= 0 || score >= 30 {
		t.Errorf("score = %d, want a small positive number", score)
	}
}

// The prompt order is the product: a host with limited patience should be
// asked for the thing that moves the score most.
func TestGapsAreOrderedByWorth(t *testing.T) {
	_, gaps := Score(Inputs{Type: TypeApartment, PhotoCount: 1})
	if len(gaps) == 0 {
		t.Fatal("expected gaps")
	}

	for i := 1; i < len(gaps); i++ {
		if gaps[i].Worth > gaps[i-1].Worth {
			t.Fatalf("gap %d (%d) outranks %d (%d)", i, gaps[i].Worth, i-1, gaps[i-1].Worth)
		}
	}
	if gaps[0].Key != "photos" {
		t.Errorf("first gap = %q, want photos", gaps[0].Key)
	}
}

func TestPartialDescriptionEarnsPartialCredit(t *testing.T) {
	none, _ := Score(Inputs{Type: TypeApartment, PhotoCount: 4})
	some, _ := Score(Inputs{Type: TypeApartment, PhotoCount: 4, Description: "Quiet gated compound."})

	if some <= none {
		t.Errorf("a short description (%d) should beat none (%d)", some, none)
	}
}

func TestMorePhotosScoreHigher(t *testing.T) {
	one, _ := Score(Inputs{Type: TypeApartment, PhotoCount: 1})
	four, _ := Score(Inputs{Type: TypeApartment, PhotoCount: 4})
	ten, _ := Score(Inputs{Type: TypeApartment, PhotoCount: 10})

	if one >= four {
		t.Errorf("one photo (%d) should score below four (%d)", one, four)
	}
	if ten != four {
		t.Errorf("past the threshold the score stops moving: %d vs %d", ten, four)
	}
}

func TestUnknownTypeScoresNothing(t *testing.T) {
	score, gaps := Score(Inputs{Type: SpaceType("submarine")})
	if score != 0 || gaps != nil {
		t.Errorf("score = %d, gaps = %v", score, gaps)
	}
}
