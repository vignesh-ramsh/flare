-- +goose Up
-- A disabled user cannot log in, their tokens stop working and open connections of theirs end,
-- but their account, tokens and access rules are kept, so enabling them again restores everything.
ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1));

-- +goose Down
ALTER TABLE users DROP COLUMN disabled;
