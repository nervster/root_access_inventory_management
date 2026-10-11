-- Show who changed a plant by name, and keep showing it after they leave the team. The name (or
-- email, when Clerk sent no name) is copied into each event when it's written, like pot names are;
-- a live lookup would go blank, since nurseries only see the users who are still their members.

-- +goose Up
ALTER TABLE plant_events ADD COLUMN changed_by_name text;

-- The current user's name or email. SECURITY DEFINER lets it read the users table directly, so it
-- takes no argument: it can only ever return the caller's own name, never look anyone else up.
-- +goose StatementBegin
CREATE FUNCTION current_user_name() RETURNS text
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT coalesce(nullif(display_name, ''), email) FROM users WHERE id = current_user_id() $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_plant_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    batch_code text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT code INTO batch_code FROM batches WHERE id = NEW.batch_id;
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'received',
                jsonb_build_object('batch', batch_code, 'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_user_id(), current_user_name());
        RETURN NEW;
    END IF;

    IF NEW.pot_size_id IS DISTINCT FROM OLD.pot_size_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'repotted',
                jsonb_build_object('fromPotSize', (SELECT name FROM pot_sizes WHERE id = OLD.pot_size_id),
                                   'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_user_id(), current_user_name());
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id,
                CASE NEW.status
                    WHEN 'for_sale' THEN 'marked_for_sale'
                    WHEN 'sold' THEN 'sold'
                    WHEN 'discarded' THEN 'discarded'
                    ELSE 'unmarked_for_sale' -- back to growing
                END::plant_event_type,
                CASE WHEN NEW.status = 'discarded' THEN jsonb_strip_nulls(jsonb_build_object('reason', NEW.discard_reason))
                     ELSE '{}'::jsonb END,
                current_user_id(), current_user_name());
    END IF;

    IF NEW.plant_name_id IS DISTINCT FROM OLD.plant_name_id OR NEW.variegation_id IS DISTINCT FROM OLD.variegation_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'name_corrected',
                jsonb_build_object(
                    'from', concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = OLD.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = OLD.variegation_id)),
                    'to',   concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = NEW.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = NEW.variegation_id))),
                current_user_id(), current_user_name());
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
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.plant_id, 'photo_added',
                jsonb_strip_nulls(jsonb_build_object('photoId', NEW.id, 'caption', NEW.caption)), current_user_id(), current_user_name());
        RETURN NEW;
    END IF;
    -- A photo removed along with its whole plant needs no event (the plant's history goes too).
    IF EXISTS (SELECT 1 FROM plants WHERE id = OLD.plant_id) THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (OLD.organization_id, OLD.plant_id, 'photo_deleted',
                jsonb_strip_nulls(jsonb_build_object('photoId', OLD.id, 'caption', OLD.caption)), current_user_id(), current_user_name());
    END IF;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

UPDATE plant_events e
SET changed_by_name = coalesce(nullif(u.display_name, ''), u.email)
FROM users u
WHERE u.id = e.changed_by;

-- +goose Down
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

DROP FUNCTION current_user_name();
ALTER TABLE plant_events DROP COLUMN changed_by_name;
