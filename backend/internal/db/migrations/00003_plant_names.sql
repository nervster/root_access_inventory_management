-- Plant names (family → genus → species → cultivar) and variegations: a shared list every nursery
-- picks from, plus each nursery's own additions.
--
-- Rows with organization_id NULL are the shared list: every nursery can read them, none can change
-- them. A nursery's own rows (organization_id set) are visible and editable only to that nursery.
-- The shared rows below come from seed/plant_names.csv and seed/variegations.csv, leaving out rows
-- still marked CHECK (they'll arrive in a later migration).

-- +goose Up
CREATE TYPE plant_rank AS ENUM ('family', 'genus', 'species', 'cultivar');

CREATE TABLE plant_names (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint REFERENCES organizations (id) ON DELETE CASCADE, -- NULL = shared list
    parent_id       bigint REFERENCES plant_names (id),                    -- NULL only for families
    rank            plant_rank  NOT NULL,
    name            text        NOT NULL, -- just this level: "Dragon Scale"
    full_name       text        NOT NULL, -- for display and search: Alocasia baginda 'Dragon Scale'
    notes           text,                 -- e.g. the trade name it's sold under
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((rank = 'family') = (parent_id IS NULL))
);
-- One entry per name under the same parent, in the shared list and within each nursery.
CREATE UNIQUE INDEX plant_names_unique_idx ON plant_names (organization_id, parent_id, name) NULLS NOT DISTINCT;
CREATE INDEX plant_names_parent_idx ON plant_names (parent_id);

CREATE TABLE variegations (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint REFERENCES organizations (id) ON DELETE CASCADE, -- NULL = shared list
    name            text        NOT NULL, -- "Aurea"
    notes           text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX variegations_unique_idx ON variegations (organization_id, name) NULLS NOT DISTINCT;

-- Nurseries read the shared rows and their own; they write only their own.
GRANT SELECT, INSERT, UPDATE, DELETE ON plant_names, variegations TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;

ALTER TABLE plant_names ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plant_names
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
CREATE POLICY shared_list ON plant_names FOR SELECT
    USING (organization_id IS NULL);

ALTER TABLE variegations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON variegations
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
CREATE POLICY shared_list ON variegations FOR SELECT
    USING (organization_id IS NULL);

-- The shared list (generated from the seed CSV files) --

INSERT INTO plant_names (rank, name, full_name) VALUES
    ('family', 'Araceae', 'Araceae'),
    ('family', 'Apocynaceae', 'Apocynaceae'),
    ('family', 'Marantaceae', 'Marantaceae'),
    ('family', 'Piperaceae', 'Piperaceae'),
    ('family', 'Moraceae', 'Moraceae'),
    ('family', 'Begoniaceae', 'Begoniaceae'),
    ('family', 'Urticaceae', 'Urticaceae'),
    ('family', 'Asparagaceae', 'Asparagaceae'),
    ('family', 'Araliaceae', 'Araliaceae'),
    ('family', 'Strelitziaceae', 'Strelitziaceae'),
    ('family', 'Commelinaceae', 'Commelinaceae'),
    ('family', 'Acanthaceae', 'Acanthaceae'),
    ('family', 'Orchidaceae', 'Orchidaceae'),
    ('family', 'Bromeliaceae', 'Bromeliaceae'),
    ('family', 'Gesneriaceae', 'Gesneriaceae'),
    ('family', 'Aspleniaceae', 'Aspleniaceae'),
    ('family', 'Nephrolepidaceae', 'Nephrolepidaceae'),
    ('family', 'Polypodiaceae', 'Polypodiaceae'),
    ('family', 'Pteridaceae', 'Pteridaceae'),
    ('family', 'Cactaceae', 'Cactaceae'),
    ('family', 'Crassulaceae', 'Crassulaceae'),
    ('family', 'Asphodelaceae', 'Asphodelaceae'),
    ('family', 'Euphorbiaceae', 'Euphorbiaceae'),
    ('family', 'Arecaceae', 'Arecaceae'),
    ('family', 'Musaceae', 'Musaceae'),
    ('family', 'Nepenthaceae', 'Nepenthaceae'),
    ('family', 'Droseraceae', 'Droseraceae');

INSERT INTO plant_names (rank, parent_id, name, full_name, notes)
SELECT 'genus', f.id, v.name, v.name, v.notes
FROM (VALUES
    ('Araceae', 'Aglaonema', NULL),
    ('Araceae', 'Alocasia', NULL),
    ('Araceae', 'Anthurium', NULL),
    ('Araceae', 'Caladium', NULL),
    ('Araceae', 'Colocasia', NULL),
    ('Araceae', 'Dieffenbachia', NULL),
    ('Araceae', 'Epipremnum', NULL),
    ('Araceae', 'Homalomena', NULL),
    ('Araceae', 'Monstera', NULL),
    ('Araceae', 'Philodendron', NULL),
    ('Araceae', 'Rhaphidophora', NULL),
    ('Araceae', 'Scindapsus', NULL),
    ('Araceae', 'Spathiphyllum', 'the peace lily'),
    ('Araceae', 'Syngonium', NULL),
    ('Araceae', 'Thaumatophyllum', 'split from Philodendron; sold under both names'),
    ('Araceae', 'Zamioculcas', 'the ZZ plant'),
    ('Apocynaceae', 'Ceropegia', NULL),
    ('Apocynaceae', 'Dischidia', NULL),
    ('Apocynaceae', 'Hoya', NULL),
    ('Marantaceae', 'Ctenanthe', NULL),
    ('Marantaceae', 'Goeppertia', NULL),
    ('Marantaceae', 'Maranta', 'the prayer plant'),
    ('Marantaceae', 'Stromanthe', NULL),
    ('Piperaceae', 'Peperomia', NULL),
    ('Moraceae', 'Ficus', NULL),
    ('Begoniaceae', 'Begonia', NULL),
    ('Urticaceae', 'Pilea', NULL),
    ('Asparagaceae', 'Chlorophytum', NULL),
    ('Araliaceae', 'Schefflera', NULL),
    ('Araliaceae', 'Fatsia', NULL),
    ('Araliaceae', 'Polyscias', NULL),
    ('Strelitziaceae', 'Strelitzia', 'the bird of paradise'),
    ('Commelinaceae', 'Tradescantia', NULL),
    ('Commelinaceae', 'Callisia', NULL),
    ('Acanthaceae', 'Fittonia', 'the nerve plant'),
    ('Acanthaceae', 'Hypoestes', 'the polka dot plant'),
    ('Orchidaceae', 'Ludisia', NULL),
    ('Orchidaceae', 'Macodes', NULL),
    ('Orchidaceae', 'Phalaenopsis', 'the moth orchid'),
    ('Bromeliaceae', 'Aechmea', NULL),
    ('Bromeliaceae', 'Cryptanthus', NULL),
    ('Bromeliaceae', 'Guzmania', NULL),
    ('Bromeliaceae', 'Neoregelia', NULL),
    ('Bromeliaceae', 'Tillandsia', 'air plants'),
    ('Bromeliaceae', 'Vriesea', NULL),
    ('Gesneriaceae', 'Aeschynanthus', 'the lipstick plant'),
    ('Gesneriaceae', 'Columnea', NULL),
    ('Gesneriaceae', 'Episcia', NULL),
    ('Aspleniaceae', 'Asplenium', NULL),
    ('Nephrolepidaceae', 'Nephrolepis', NULL),
    ('Polypodiaceae', 'Microsorum', NULL),
    ('Polypodiaceae', 'Platycerium', 'staghorn ferns'),
    ('Pteridaceae', 'Adiantum', 'maidenhair ferns'),
    ('Cactaceae', 'Epiphyllum', NULL),
    ('Cactaceae', 'Rhipsalis', NULL),
    ('Cactaceae', 'Schlumbergera', 'the holiday cactus'),
    ('Crassulaceae', 'Crassula', NULL),
    ('Crassulaceae', 'Echeveria', NULL),
    ('Crassulaceae', 'Kalanchoe', NULL),
    ('Crassulaceae', 'Sedum', NULL),
    ('Asphodelaceae', 'Aloe', NULL),
    ('Asphodelaceae', 'Gasteria', NULL),
    ('Euphorbiaceae', 'Codiaeum', 'the croton'),
    ('Euphorbiaceae', 'Euphorbia', NULL),
    ('Arecaceae', 'Chamaedorea', NULL),
    ('Arecaceae', 'Dypsis', NULL),
    ('Arecaceae', 'Howea', NULL),
    ('Musaceae', 'Musa', NULL),
    ('Nepenthaceae', 'Nepenthes', 'tropical pitcher plants'),
    ('Droseraceae', 'Dionaea', NULL),
    ('Droseraceae', 'Drosera', 'sundews')
) AS v(family, name, notes)
JOIN plant_names f ON f.rank = 'family' AND f.name = v.family AND f.organization_id IS NULL;

INSERT INTO plant_names (rank, parent_id, name, full_name, notes)
SELECT 'species', g.id, v.name, g.name || ' ' || v.name, v.notes
FROM (VALUES
    ('Aglaonema', 'commutatum', NULL),
    ('Alocasia', 'cuprea', NULL),
    ('Alocasia', 'lauterbachiana', NULL),
    ('Alocasia', 'macrorrhizos', NULL),
    ('Alocasia', 'melo', NULL),
    ('Alocasia', 'odora', NULL),
    ('Alocasia', 'sanderiana', NULL),
    ('Alocasia', 'scalprum', NULL),
    ('Alocasia', 'sinuata', NULL),
    ('Alocasia', 'wentii', NULL),
    ('Alocasia', 'zebrina', NULL),
    ('Anthurium', 'andraeanum', NULL),
    ('Anthurium', 'clarinervium', NULL),
    ('Anthurium', 'crystallinum', NULL),
    ('Anthurium', 'forgetii', NULL),
    ('Anthurium', 'luxurians', NULL),
    ('Anthurium', 'magnificum', NULL),
    ('Anthurium', 'pallidiflorum', NULL),
    ('Anthurium', 'papillilaminum', NULL),
    ('Anthurium', 'regale', NULL),
    ('Anthurium', 'veitchii', NULL),
    ('Anthurium', 'vittarifolium', NULL),
    ('Anthurium', 'warocqueanum', NULL),
    ('Colocasia', 'esculenta', NULL),
    ('Dieffenbachia', 'seguine', NULL),
    ('Epipremnum', 'aureum', 'the common pothos'),
    ('Epipremnum', 'pinnatum', NULL),
    ('Monstera', 'adansonii', NULL),
    ('Monstera', 'deliciosa', NULL),
    ('Monstera', 'dubia', NULL),
    ('Monstera', 'obliqua', NULL),
    ('Monstera', 'pinnatipartita', NULL),
    ('Monstera', 'siltepecana', NULL),
    ('Monstera', 'standleyana', NULL),
    ('Monstera', 'subpinnata', NULL),
    ('Philodendron', 'billietiae', NULL),
    ('Philodendron', 'bipennifolium', NULL),
    ('Philodendron', 'brandtianum', NULL),
    ('Philodendron', 'gloriosum', NULL),
    ('Philodendron', 'hederaceum', 'the heartleaf philodendron'),
    ('Philodendron', 'mamei', NULL),
    ('Philodendron', 'melanochrysum', NULL),
    ('Philodendron', 'pedatum', NULL),
    ('Philodendron', 'plowmanii', NULL),
    ('Philodendron', 'sodiroi', NULL),
    ('Philodendron', 'squamiferum', NULL),
    ('Philodendron', 'tortum', NULL),
    ('Philodendron', 'verrucosum', NULL),
    ('Rhaphidophora', 'cryptantha', NULL),
    ('Rhaphidophora', 'foraminifera', NULL),
    ('Rhaphidophora', 'hayi', NULL),
    ('Rhaphidophora', 'tetrasperma', 'sold as "Mini Monstera"'),
    ('Scindapsus', 'pictus', NULL),
    ('Spathiphyllum', 'wallisii', NULL),
    ('Syngonium', 'erythrophyllum', NULL),
    ('Syngonium', 'podophyllum', NULL),
    ('Syngonium', 'wendlandii', NULL),
    ('Thaumatophyllum', 'bipinnatifidum', 'sold as "Philodendron Selloum"'),
    ('Thaumatophyllum', 'xanadu', 'sold as "Philodendron Xanadu"'),
    ('Zamioculcas', 'zamiifolia', NULL),
    ('Ceropegia', 'woodii', 'the string of hearts'),
    ('Hoya', 'australis', NULL),
    ('Hoya', 'carnosa', NULL),
    ('Hoya', 'curtisii', NULL),
    ('Hoya', 'kentiana', NULL),
    ('Hoya', 'kerrii', NULL),
    ('Hoya', 'krohniana', NULL),
    ('Hoya', 'linearis', NULL),
    ('Hoya', 'macrophylla', NULL),
    ('Hoya', 'obovata', NULL),
    ('Hoya', 'polyneura', NULL),
    ('Hoya', 'pubicalyx', NULL),
    ('Ctenanthe', 'burle-marxii', NULL),
    ('Goeppertia', 'lancifolia', 'sold as "Calathea Rattlesnake"'),
    ('Goeppertia', 'makoyana', 'sold as "Calathea Peacock"'),
    ('Goeppertia', 'orbifolia', 'sold as "Calathea Orbifolia"'),
    ('Maranta', 'leuconeura', NULL),
    ('Peperomia', 'argyreia', 'the watermelon peperomia'),
    ('Peperomia', 'caperata', NULL),
    ('Peperomia', 'obtusifolia', NULL),
    ('Peperomia', 'polybotrya', NULL),
    ('Peperomia', 'prostrata', NULL),
    ('Peperomia', 'rotundifolia', NULL),
    ('Peperomia', 'scandens', NULL),
    ('Ficus', 'benjamina', NULL),
    ('Ficus', 'elastica', 'the rubber plant'),
    ('Ficus', 'lyrata', 'the fiddle-leaf fig'),
    ('Ficus', 'pumila', NULL),
    ('Ficus', 'triangularis', NULL),
    ('Begonia', 'amphioxus', NULL),
    ('Begonia', 'darthvaderiana', NULL),
    ('Begonia', 'maculata', NULL),
    ('Pilea', 'cadierei', NULL),
    ('Pilea', 'glauca', NULL),
    ('Pilea', 'peperomioides', 'the Chinese money plant'),
    ('Chlorophytum', 'comosum', 'the spider plant'),
    ('Schefflera', 'arboricola', NULL),
    ('Fatsia', 'japonica', NULL),
    ('Strelitzia', 'nicolai', NULL),
    ('Strelitzia', 'reginae', NULL),
    ('Tradescantia', 'fluminensis', NULL),
    ('Tradescantia', 'spathacea', NULL),
    ('Tradescantia', 'zebrina', NULL),
    ('Fittonia', 'albivenis', NULL),
    ('Ludisia', 'discolor', 'the jewel orchid'),
    ('Asplenium', 'nidus', 'the bird''s nest fern'),
    ('Nephrolepis', 'exaltata', 'the Boston fern'),
    ('Platycerium', 'bifurcatum', NULL),
    ('Adiantum', 'raddianum', NULL),
    ('Rhipsalis', 'baccifera', NULL),
    ('Crassula', 'ovata', 'the jade plant'),
    ('Aloe', 'vera', NULL),
    ('Codiaeum', 'variegatum', NULL),
    ('Euphorbia', 'trigona', NULL),
    ('Chamaedorea', 'elegans', 'the parlor palm'),
    ('Dypsis', 'lutescens', 'the areca palm'),
    ('Howea', 'forsteriana', 'the kentia palm'),
    ('Dionaea', 'muscipula', 'the Venus flytrap'),
    ('Aglaonema', 'pictum', NULL),
    ('Alocasia', 'baginda', NULL),
    ('Alocasia', 'micholitziana', NULL),
    ('Alocasia', 'reginula', NULL),
    ('Philodendron', 'erubescens', NULL),
    ('Scindapsus', 'treubii', NULL),
    ('Goeppertia', 'roseopicta', NULL),
    ('Stromanthe', 'sanguinea', NULL),
    ('Peperomia', 'clusiifolia', NULL)
) AS v(genus, name, notes)
JOIN plant_names g ON g.rank = 'genus' AND g.name = v.genus AND g.organization_id IS NULL;

-- Cultivars of a species, e.g. Alocasia baginda 'Dragon Scale'.
INSERT INTO plant_names (rank, parent_id, name, full_name, notes)
SELECT 'cultivar', s.id, v.name, s.full_name || ' ''' || v.name || '''', v.notes
FROM (VALUES
    ('Aglaonema', 'commutatum', 'Silver Bay', NULL),
    ('Aglaonema', 'pictum', 'Tricolor', NULL),
    ('Alocasia', 'baginda', 'Dragon Scale', 'sold as "Alocasia Dragon Scale"'),
    ('Alocasia', 'baginda', 'Silver Dragon', 'sold as "Alocasia Silver Dragon"'),
    ('Alocasia', 'cuprea', 'Red Secret', NULL),
    ('Alocasia', 'macrorrhizos', 'Stingray', NULL),
    ('Alocasia', 'micholitziana', 'Frydek', 'sold as "Alocasia Frydek"'),
    ('Alocasia', 'reginula', 'Black Velvet', NULL),
    ('Colocasia', 'esculenta', 'Black Magic', NULL),
    ('Epipremnum', 'aureum', 'Global Green', NULL),
    ('Epipremnum', 'aureum', 'Manjula', NULL),
    ('Epipremnum', 'aureum', 'Marble Queen', NULL),
    ('Epipremnum', 'aureum', 'N''Joy', NULL),
    ('Epipremnum', 'aureum', 'Neon', NULL),
    ('Epipremnum', 'aureum', 'Snow Queen', NULL),
    ('Epipremnum', 'pinnatum', 'Cebu Blue', NULL),
    ('Monstera', 'deliciosa', 'Thai Constellation', 'variegation is part of this cultivar'),
    ('Philodendron', 'erubescens', 'Pink Princess', NULL),
    ('Philodendron', 'hederaceum', 'Brasil', NULL),
    ('Scindapsus', 'pictus', 'Argyraeus', NULL),
    ('Scindapsus', 'pictus', 'Exotica', NULL),
    ('Scindapsus', 'treubii', 'Dark Form', NULL),
    ('Scindapsus', 'treubii', 'Moonlight', NULL),
    ('Syngonium', 'podophyllum', 'Neon Robusta', NULL),
    ('Syngonium', 'podophyllum', 'Pink Splash', NULL),
    ('Syngonium', 'podophyllum', 'White Butterfly', NULL),
    ('Zamioculcas', 'zamiifolia', 'Raven', NULL),
    ('Hoya', 'carnosa', 'Compacta', NULL),
    ('Hoya', 'carnosa', 'Krimson Princess', NULL),
    ('Hoya', 'carnosa', 'Krimson Queen', NULL),
    ('Goeppertia', 'roseopicta', 'Dottie', NULL),
    ('Goeppertia', 'roseopicta', 'Medallion', NULL),
    ('Maranta', 'leuconeura', 'Kerchoveana', NULL),
    ('Maranta', 'leuconeura', 'Lemon Lime', NULL),
    ('Stromanthe', 'sanguinea', 'Triostar', NULL),
    ('Peperomia', 'caperata', 'Rosso', NULL),
    ('Peperomia', 'clusiifolia', 'Ginny', NULL),
    ('Ficus', 'elastica', 'Burgundy', NULL),
    ('Ficus', 'elastica', 'Ruby', NULL),
    ('Ficus', 'elastica', 'Tineke', NULL),
    ('Chlorophytum', 'comosum', 'Vittatum', NULL)
) AS v(genus, species, name, notes)
JOIN plant_names g ON g.rank = 'genus' AND g.name = v.genus AND g.organization_id IS NULL
JOIN plant_names s ON s.rank = 'species' AND s.parent_id = g.id AND s.name = v.species;

-- Hybrid cultivars directly under a genus, e.g. Alocasia 'Polly'.
INSERT INTO plant_names (rank, parent_id, name, full_name, notes)
SELECT 'cultivar', g.id, v.name, g.name || ' ''' || v.name || '''', v.notes
FROM (VALUES
    ('Aglaonema', 'Red Valentine', 'hybrid'),
    ('Alocasia', 'Polly', 'hybrid (Alocasia × amazonica); sold as "Alocasia Polly"'),
    ('Alocasia', 'Maharani', 'hybrid'),
    ('Alocasia', 'Regal Shields', 'hybrid'),
    ('Anthurium', 'Crystal Hope', 'hybrid'),
    ('Philodendron', 'Birkin', 'hybrid'),
    ('Philodendron', 'Florida Ghost', 'hybrid'),
    ('Philodendron', 'Painted Lady', 'hybrid'),
    ('Philodendron', 'Paraiso Verde', 'hybrid'),
    ('Philodendron', 'Prince of Orange', 'hybrid'),
    ('Philodendron', 'Ring of Fire', 'hybrid'),
    ('Philodendron', 'White Knight', 'hybrid'),
    ('Philodendron', 'White Princess', 'hybrid'),
    ('Hoya', 'Mathilde', 'hybrid'),
    ('Hoya', 'Sunrise', 'hybrid')
) AS v(genus, name, notes)
JOIN plant_names g ON g.rank = 'genus' AND g.name = v.genus AND g.organization_id IS NULL;

INSERT INTO variegations (name, notes) VALUES
    ('Albo', 'white variegation (albo-variegata)'),
    ('Aurea', 'yellow or golden variegation'),
    ('Mint', 'pale mint-green variegation'),
    ('Tricolor', 'three colors, usually green, white, and pink'),
    ('Half-moon', 'pattern: half the leaf variegated, split down the midrib'),
    ('Marginata', 'pattern: variegated leaf edges');


-- +goose Down
DROP TABLE variegations;
DROP TABLE plant_names;
DROP TYPE plant_rank;
