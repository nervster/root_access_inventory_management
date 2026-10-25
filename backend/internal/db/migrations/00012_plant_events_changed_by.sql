-- Names the history's "who" column changed_by, like batches.created_by and plant_photos.uploaded_by.
-- 00011 is already applied, so the rename happens here: the column, the function that reads who is
-- acting (app.user_id), and the two trigger functions that write the column.

-- +goose Up
ALTER TABLE plant_events RENAME COLUMN actor_id TO changed_by;
ALTER FUNCTION current_actor_id() RENAME TO current_user_id;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_plant_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    batch_code text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT code INTO batch_code FROM batches WHERE id = NEW.batch_id;
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by)
        VALUES (NEW.organization_id, NEW.id, 'received',
                jsonb_build_object('batch', batch_code, 'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_user_id());
        RETURN NEW;
    END IF;

    IF NEW.pot_size_id IS DISTINCT FROM OLD.pot_size_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by)
        VALUES (NEW.organization_id, NEW.id, 'repotted',
                jsonb_build_object('fromPotSize', (SELECT name FROM pot_sizes WHERE id = OLD.pot_size_id),
                                   'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_user_id());
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by)
        VALUES (NEW.organization_id, NEW.id,
                CASE NEW.status
                    WHEN 'for_sale' THEN 'marked_for_sale'
                    WHEN 'sold' THEN 'sold'
                    WHEN 'discarded' THEN 'discarded'
                    ELSE 'unmarked_for_sale' -- back to growing
                END::plant_event_type,
                CASE WHEN NEW.status = 'discarded' THEN jsonb_strip_nulls(jsonb_build_object('reason', NEW.discard_reason))
                     ELSE '{}'::jsonb END,
                current_user_id());
    END IF;

    IF NEW.plant_name_id IS DISTINCT FROM OLD.plant_name_id OR NEW.variegation_id IS DISTINCT FROM OLD.variegation_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by)
        VALUES (NEW.organization_id, NEW.id, 'name_corrected',
                jsonb_build_object(
                    'from', concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = OLD.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = OLD.variegation_id)),
                    'to',   concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = NEW.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = NEW.variegation_id))),
                current_user_id());
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_plant_photo_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by)
        VALUES (NEW.organization_id, NEW.plant_id, 'photo_added',
                jsonb_strip_nulls(jsonb_build_object('photoId', NEW.id, 'caption', NEW.caption)), current_user_id());
        RETURN NEW;
    END IF;
    -- A photo removed along with its whole plant needs no event (the plant's history goes too).
    IF EXISTS (SELECT 1 FROM plants WHERE id = OLD.plant_id) THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by)
        VALUES (OLD.organization_id, OLD.plant_id, 'photo_deleted',
                jsonb_strip_nulls(jsonb_build_object('photoId', OLD.id, 'caption', OLD.caption)), current_user_id());
    END IF;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE plant_events RENAME COLUMN changed_by TO actor_id;
ALTER FUNCTION current_user_id() RENAME TO current_actor_id;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_plant_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    batch_code text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT code INTO batch_code FROM batches WHERE id = NEW.batch_id;
        INSERT INTO plant_events (organization_id, plant_id, type, details, actor_id)
        VALUES (NEW.organization_id, NEW.id, 'received',
                jsonb_build_object('batch', batch_code, 'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_actor_id());
        RETURN NEW;
    END IF;

    IF NEW.pot_size_id IS DISTINCT FROM OLD.pot_size_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, actor_id)
        VALUES (NEW.organization_id, NEW.id, 'repotted',
                jsonb_build_object('fromPotSize', (SELECT name FROM pot_sizes WHERE id = OLD.pot_size_id),
                                   'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_actor_id());
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, actor_id)
        VALUES (NEW.organization_id, NEW.id,
                CASE NEW.status
                    WHEN 'for_sale' THEN 'marked_for_sale'
                    WHEN 'sold' THEN 'sold'
                    WHEN 'discarded' THEN 'discarded'
                    ELSE 'unmarked_for_sale' -- back to growing
                END::plant_event_type,
                CASE WHEN NEW.status = 'discarded' THEN jsonb_strip_nulls(jsonb_build_object('reason', NEW.discard_reason))
                     ELSE '{}'::jsonb END,
                current_actor_id());
    END IF;

    IF NEW.plant_name_id IS DISTINCT FROM OLD.plant_name_id OR NEW.variegation_id IS DISTINCT FROM OLD.variegation_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, actor_id)
        VALUES (NEW.organization_id, NEW.id, 'name_corrected',
                jsonb_build_object(
                    'from', concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = OLD.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = OLD.variegation_id)),
                    'to',   concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = NEW.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = NEW.variegation_id))),
                current_actor_id());
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_plant_photo_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, actor_id)
        VALUES (NEW.organization_id, NEW.plant_id, 'photo_added',
                jsonb_strip_nulls(jsonb_build_object('photoId', NEW.id, 'caption', NEW.caption)), current_actor_id());
        RETURN NEW;
    END IF;
    -- A photo removed along with its whole plant needs no event (the plant's history goes too).
    IF EXISTS (SELECT 1 FROM plants WHERE id = OLD.plant_id) THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, actor_id)
        VALUES (OLD.organization_id, OLD.plant_id, 'photo_deleted',
                jsonb_strip_nulls(jsonb_build_object('photoId', OLD.id, 'caption', OLD.caption)), current_actor_id());
    END IF;
    RETURN OLD;
END
$$;
-- +goose StatementEnd
