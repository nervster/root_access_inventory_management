-- name: UpsertUser :one
-- Creates the user on first sign-in, or refreshes their email and name from the session token.
-- A missing name keeps the one already saved.
INSERT INTO users (external_id, email, display_name)
VALUES (@external_id, @email, @display_name)
ON CONFLICT (external_id) DO UPDATE
SET email        = EXCLUDED.email,
    display_name = COALESCE(EXCLUDED.display_name, users.display_name)
RETURNING *;

-- name: GetUserByExternalID :one
SELECT * FROM users
WHERE external_id = @external_id;
