-- Who added a photo and who sent an invitation, by name (or email when Clerk sent no name): copied
-- in when the row is written, like plant history (00013), so it still shows after they leave.

-- +goose Up
ALTER TABLE plant_photos ADD COLUMN uploaded_by_name text;
ALTER TABLE invitations ADD COLUMN invited_by_name text;

UPDATE plant_photos ph
SET uploaded_by_name = coalesce(nullif(u.display_name, ''), u.email)
FROM users u
WHERE u.id = ph.uploaded_by;

UPDATE invitations i
SET invited_by_name = coalesce(nullif(u.display_name, ''), u.email)
FROM users u
WHERE u.id = i.invited_by_user_id;

-- +goose Down
ALTER TABLE invitations DROP COLUMN invited_by_name;
ALTER TABLE plant_photos DROP COLUMN uploaded_by_name;
