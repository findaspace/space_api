package listing

import (
	"slices"
	"strings"
)

// The four things a listing can carry, and what each is worth out of 100.
// Photos dominate because a listing without them does not convert, and the
// walkthrough is weighted heavily because it is the direct answer to the
// complaint that photographs are not of the actual room.
const (
	weightPhotos      = 30
	weightVideo       = 15
	weightDescription = 10
	weightAttributes  = 45
)

const (
	photosForFull     = 4
	descriptionTarget = 60
)

// Gap is one thing a host could add, with what it would be worth. The host's
// listing screen shows these in order: the biggest win first.
type Gap struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Worth int    `json:"worth"`
}

type Inputs struct {
	Type        SpaceType
	Attributes  map[string]any
	Description string
	PhotoCount  int
	HasVideo    bool
}

// Score returns the completeness percentage and what is missing, ordered by
// what each gap is worth. Nothing here blocks publishing; it feeds ranking and
// the prompts, which is the carrot that replaced the gate.
func Score(in Inputs) (int, []Gap) {
	schema, ok := SchemaFor(in.Type)
	if !ok {
		return 0, nil
	}

	var gaps []Gap
	total := 0

	photos := in.PhotoCount
	if photos > photosForFull {
		photos = photosForFull
	}
	if photos < 0 {
		photos = 0
	}
	earnedPhotos := weightPhotos * photos / photosForFull
	total += earnedPhotos
	if in.PhotoCount < photosForFull {
		gaps = append(gaps, Gap{
			Key:   "photos",
			Label: "Add more photos",
			Worth: weightPhotos - earnedPhotos,
		})
	}

	if in.HasVideo {
		total += weightVideo
	} else {
		gaps = append(gaps, Gap{Key: "video", Label: "Add a walkthrough video", Worth: weightVideo})
	}

	described := len(strings.TrimSpace(in.Description))
	if described > descriptionTarget {
		described = descriptionTarget
	}
	// Partial credit: a host who wrote one line should see the score move.
	earnedDesc := weightDescription * described / descriptionTarget
	total += earnedDesc
	if earnedDesc < weightDescription {
		gaps = append(gaps, Gap{
			Key:   "description",
			Label: "Describe the place",
			Worth: weightDescription - earnedDesc,
		})
	}

	var filled, possible int
	missing := make([]Field, 0, len(schema.Fields))

	for _, f := range schema.Fields {
		if f.Weight <= 0 {
			continue
		}
		possible += f.Weight

		if v, ok := in.Attributes[f.Key]; ok && v != nil {
			filled += f.Weight
			continue
		}
		missing = append(missing, f)
	}

	if possible > 0 {
		total += weightAttributes * filled / possible

		// An attribute's worth depends on the whole schema, so it can only be
		// priced once every field has been counted.
		for _, f := range missing {
			gaps = append(gaps, Gap{
				Key:   f.Key,
				Label: f.Label,
				Worth: weightAttributes * f.Weight / possible,
			})
		}
	}

	if total > 100 {
		total = 100
	}

	slices.SortStableFunc(gaps, func(a, b Gap) int { return b.Worth - a.Worth })
	return total, gaps
}
