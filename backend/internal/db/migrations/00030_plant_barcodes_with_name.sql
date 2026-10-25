-- Barcodes now say which plant they are: RAH-ADF-000123 (nursery, plant's short code, running
-- number). The short code is the first letter of each word of the plant's name (Anthurium 'Delta
-- Force' → ADF), or the first three letters when the name is one word (Anthurium → ANT).
-- It is made when the plant is received and never changes if the plant's name is corrected later.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION plant_short_code(full_name text) RETURNS text
    LANGUAGE sql IMMUTABLE
    AS $$
    SELECT CASE count(*)
               WHEN 0 THEN 'PLT'
               WHEN 1 THEN upper(left(max(word), 3))
               ELSE upper(left(string_agg(left(word, 1), '' ORDER BY position), 4))
           END
    FROM regexp_split_to_table(regexp_replace(full_name, '[^[:alpha:] ]', '', 'g'), ' +')
             WITH ORDINALITY AS words (word, position)
    WHERE word <> ''
$$;
-- +goose StatementEnd

-- Existing plants get the new form, using their name as it is now. The history trigger and the
-- Shopify stock trigger only react to pot, status, and name changes, so this adds no events.
UPDATE plants p
SET barcode = regexp_replace(p.barcode, '^([^-]+)-', '\1-' || plant_short_code(pn.full_name) || '-')
FROM plant_names pn
WHERE pn.id = p.plant_name_id
  AND p.barcode ~ '^[^-]+-[0-9]+$';

-- +goose Down
UPDATE plants SET barcode = regexp_replace(barcode, '^([^-]+)-[^-]+-', '\1-')
WHERE barcode ~ '^[^-]+-[^-]+-[0-9]+$';
DROP FUNCTION plant_short_code(text);
