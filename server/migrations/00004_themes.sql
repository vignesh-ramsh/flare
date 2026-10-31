-- +goose Up
-- The themes this server offers to its apps: a name, light or dark, and the colours
-- the theme sets. An admin adds them from the Admin UI; apps download them and offer
-- them next to their own.
--
-- name_key is the name in lower case: names are unique whatever their case, so
-- "Midnight" and "midnight" are one theme. colors is a JSON object of token name to
-- "#RRGGBB"; the application checks it, and it holds only what the theme sets.
CREATE TABLE themes (
    name_key   TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    brightness TEXT    NOT NULL CHECK (brightness IN ('light', 'dark')),
    colors     TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE themes;
