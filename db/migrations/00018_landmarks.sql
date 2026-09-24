-- +goose Up
CREATE TYPE landmark_kind AS ENUM (
    'campus_gate', 'market', 'transport_hub', 'mall', 'junction', 'hospital'
);

CREATE TABLE landmarks (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL,
    kind       landmark_kind NOT NULL,
    locality   text NOT NULL,
    region     text NOT NULL DEFAULT 'Greater Accra',
    latitude   double precision NOT NULL,
    longitude  double precision NOT NULL,
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT landmarks_lat_range CHECK (latitude BETWEEN -90 AND 90),
    CONSTRAINT landmarks_lng_range CHECK (longitude BETWEEN -180 AND 180),
    CONSTRAINT landmarks_unique UNIQUE (name, locality)
);

-- Same expression as migration 00004, character for character, so the prefilter
-- that picks nearby landmarks uses an index instead of scanning the table.
CREATE INDEX landmarks_geo_idx ON landmarks
    USING gist ((ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geography))
    WHERE active;

CREATE TABLE space_landmarks (
    space_id     uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    landmark_id  uuid NOT NULL REFERENCES landmarks(id) ON DELETE CASCADE,
    walk_seconds integer NOT NULL,
    walk_metres  integer NOT NULL,
    computed_at  timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (space_id, landmark_id),
    CONSTRAINT space_landmarks_walk_seconds CHECK (walk_seconds >= 0 AND walk_seconds <= 14400),
    CONSTRAINT space_landmarks_walk_metres CHECK (walk_metres >= 0)
);

-- Search reads the nearest landmark per listing, so the index leads with the
-- space and orders by time.
CREATE INDEX space_landmarks_nearest_idx ON space_landmarks (space_id, walk_seconds);

-- Listings whose walking times have never been computed, for the backfill job.
CREATE INDEX spaces_unrouted_idx ON spaces (published_at)
    WHERE status = 'published';

-- Starter landmark data.
--
-- THESE COORDINATES ARE APPROXIMATE and must be corrected against the real
-- thing before launch. A gate pinned 200 metres from the actual gate produces
-- a walking time that is quietly wrong for every listing near it, and nobody
-- will notice because the number still looks plausible.
INSERT INTO landmarks (name, kind, locality, region, latitude, longitude) VALUES
    ('University of Ghana Main Gate', 'campus_gate', 'Legon', 'Greater Accra', 5.6508, -0.1870),
    ('KNUST Main Gate', 'campus_gate', 'Ayeduase', 'Ashanti', 6.6745, -1.5716),
    ('University of Cape Coast Main Gate', 'campus_gate', 'Cape Coast', 'Central', 5.1130, -1.2900),
    ('Madina Market', 'market', 'Madina', 'Greater Accra', 5.6836, -0.1667),
    ('Kaneshie Market', 'market', 'Kaneshie', 'Greater Accra', 5.5647, -0.2320),
    ('Makola Market', 'market', 'Accra Central', 'Greater Accra', 5.5470, -0.2100),
    ('Kejetia Market', 'market', 'Kumasi', 'Ashanti', 6.6930, -1.6220),
    ('Kwame Nkrumah Interchange', 'junction', 'Circle', 'Greater Accra', 5.5710, -0.2100),
    ('Atomic Junction', 'junction', 'Haatso', 'Greater Accra', 5.6620, -0.1980),
    ('Tetteh Quarshie Interchange', 'junction', 'Airport Residential', 'Greater Accra', 5.6180, -0.1770),
    ('Accra Mall', 'mall', 'Tetteh Quarshie', 'Greater Accra', 5.6197, -0.1717),
    ('A&C Mall', 'mall', 'East Legon', 'Greater Accra', 5.6363, -0.1580),
    ('West Hills Mall', 'mall', 'Weija', 'Greater Accra', 5.5540, -0.3420),
    ('Kotoka International Airport', 'transport_hub', 'Airport', 'Greater Accra', 5.6052, -0.1668),
    ('Tema Station', 'transport_hub', 'Accra Central', 'Greater Accra', 5.5490, -0.2020),
    ('Korle Bu Teaching Hospital', 'hospital', 'Korle Bu', 'Greater Accra', 5.5360, -0.2260),
    ('Komfo Anokye Teaching Hospital', 'hospital', 'Bantama', 'Ashanti', 6.6970, -1.6300);

-- +goose Down
DROP INDEX IF EXISTS spaces_unrouted_idx;
DROP TABLE IF EXISTS space_landmarks;
DROP TABLE IF EXISTS landmarks;
DROP TYPE IF EXISTS landmark_kind;
