-- +goose Up
ALTER TABLE spaces DROP CONSTRAINT spaces_period_matches_mode;
ALTER TABLE spaces DROP CONSTRAINT spaces_mode_allowed_for_type;
ALTER TABLE spaces ADD CONSTRAINT spaces_period_matches_mode CHECK (
 (rental_mode='nightly' AND price_period IN ('night','week')) OR
 (rental_mode='term' AND (price_period IN ('month','year') OR (space_type='hostel_bed' AND price_period IN ('semester','academic_year')))) OR
 (rental_mode='flexible' AND price_period IN ('hour','day','week'))
);
ALTER TABLE spaces ADD CONSTRAINT spaces_mode_allowed_for_type CHECK (
 CASE
 WHEN space_type IN ('football_pitch','sports_court','sports_facility') THEN rental_mode='flexible'
 WHEN space_type IN ('studio','office') THEN rental_mode IN ('term','flexible')
 WHEN space_type IN ('shop','warehouse','land') THEN rental_mode='term'
 WHEN space_type='event_space' THEN rental_mode IN ('nightly','flexible')
 WHEN space_type='parking' THEN true
 ELSE rental_mode IN ('nightly','term') END
);

-- +goose Down
ALTER TABLE spaces DROP CONSTRAINT spaces_period_matches_mode;
ALTER TABLE spaces DROP CONSTRAINT spaces_mode_allowed_for_type;
ALTER TABLE spaces ADD CONSTRAINT spaces_period_matches_mode CHECK (
 (rental_mode='nightly' AND price_period IN ('night','week')) OR
 (rental_mode='term' AND price_period IN ('month','year'))
);
ALTER TABLE spaces ADD CONSTRAINT spaces_mode_allowed_for_type CHECK (
 CASE WHEN space_type IN ('shop','office','warehouse','land') THEN rental_mode='term'
 WHEN space_type='event_space' THEN rental_mode='nightly' ELSE true END
);
