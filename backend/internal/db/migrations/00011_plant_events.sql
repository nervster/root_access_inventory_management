-- Each plant's life story (an audit log): received, repotted, marked for sale, discarded, photos…
-- Postgres triggers write the events whenever a plant or its photos change, so no part of the app
-- can forget to. Who did it comes from the setting app.user_id, which db.InTenant sets per request.
-- Names (pot sizes, plants) are copied into the event, so history reads right after renames.
--
-- This replaces pot_changes: its rows become 'received' and 'repotted' events, then it's dropped.

-- +goose Up
CREATE TYPE plant_event_type AS ENUM (
    'received', 'repotted', 'marked_for_sale', 'unmarked_for_sale', 'sold', 'discarded',
    'name_corrected', 'photo_added', 'photo_deleted'
);

CREATE TABLE plant_events (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint           NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plant_id        bigint           NOT NULL,
    type            plant_event_type NOT NULL,
    details         jsonb            NOT NULL DEFAULT '{}', -- e.g. {"fromPotSize": "Small Cup", "toPotSize": "Medium Cup"}
    actor_id        bigint           REFERENCES users (id) ON DELETE SET NULL, -- who; NULL for system changes
    occurred_at     timestamptz      NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, plant_id) REFERENCES plants (organization_id, id) ON DELETE CASCADE
);
CREATE INDEX plant_events_plant_idx ON plant_events (plant_id, occurred_at, id);

GRANT SELECT, INSERT ON plant_events TO nms_tenant; -- history is never edited or deleted by the app
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE plant_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plant_events
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- The user acting in this transaction (set by db.InTenant), or NULL.
CREATE FUNCTION current_actor_id() RETURNS bigint
    LANGUAGE sql STABLE
    AS $$ SELECT NULLIF(current_setting('app.user_id', true), '')::bigint $$;

-- +goose StatementBegin
CREATE FUNCTION record_plant_event() RETURNS trigger
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

CREATE TRIGGER plant_events_on_change
    AFTER INSERT OR UPDATE ON plants
    FOR EACH ROW EXECUTE FUNCTION record_plant_event();

-- +goose StatementBegin
CREATE FUNCTION record_plant_photo_event() RETURNS trigger
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

CREATE TRIGGER plant_events_on_photo
    AFTER INSERT OR DELETE ON plant_photos
    FOR EACH ROW EXECUTE FUNCTION record_plant_photo_event();

-- History so far: pot moves (first pot = received), discards, and photos. When existing for-sale
-- plants went on sale wasn't recorded, so their story starts without that event.
INSERT INTO plant_events (organization_id, plant_id, type, details, actor_id, occurred_at)
SELECT pc.organization_id, pc.plant_id,
       CASE WHEN pc.from_pot_size_id IS NULL THEN 'received' ELSE 'repotted' END::plant_event_type,
       CASE WHEN pc.from_pot_size_id IS NULL
            THEN jsonb_build_object('batch', b.code, 'toPotSize', to_ps.name)
            ELSE jsonb_build_object('fromPotSize', from_ps.name, 'toPotSize', to_ps.name) END,
       pc.changed_by, pc.changed_at
FROM pot_changes pc
JOIN plants p ON p.id = pc.plant_id
JOIN batches b ON b.id = p.batch_id
LEFT JOIN pot_sizes from_ps ON from_ps.id = pc.from_pot_size_id
JOIN pot_sizes to_ps ON to_ps.id = pc.to_pot_size_id
ORDER BY pc.changed_at, pc.id;

INSERT INTO plant_events (organization_id, plant_id, type, details, occurred_at)
SELECT organization_id, id, 'discarded', jsonb_strip_nulls(jsonb_build_object('reason', discard_reason)), discarded_at
FROM plants
WHERE status = 'discarded' AND discarded_at IS NOT NULL;

INSERT INTO plant_events (organization_id, plant_id, type, details, actor_id, occurred_at)
SELECT organization_id, plant_id, 'photo_added', jsonb_strip_nulls(jsonb_build_object('photoId', id, 'caption', caption)),
       uploaded_by, created_at
FROM plant_photos;

DROP TABLE pot_changes;

-- +goose Down
CREATE TABLE pot_changes (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id  bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plant_id         bigint      NOT NULL,
    from_pot_size_id bigint,
    to_pot_size_id   bigint      NOT NULL,
    changed_at       timestamptz NOT NULL DEFAULT now(),
    changed_by       bigint      REFERENCES users (id) ON DELETE SET NULL,
    FOREIGN KEY (organization_id, plant_id) REFERENCES plants (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, from_pot_size_id) REFERENCES pot_sizes (organization_id, id),
    FOREIGN KEY (organization_id, to_pot_size_id) REFERENCES pot_sizes (organization_id, id)
);
CREATE INDEX pot_changes_plant_idx ON pot_changes (plant_id, changed_at);
GRANT SELECT, INSERT, UPDATE, DELETE ON pot_changes TO nms_tenant;
ALTER TABLE pot_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pot_changes
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
-- Pot history (by pot name) can't be turned back into pot IDs reliably; pot_changes restarts empty.

DROP TRIGGER plant_events_on_photo ON plant_photos;
DROP TRIGGER plant_events_on_change ON plants;
DROP FUNCTION record_plant_photo_event();
DROP FUNCTION record_plant_event();
DROP FUNCTION current_actor_id();
DROP TABLE plant_events;
DROP TYPE plant_event_type;
