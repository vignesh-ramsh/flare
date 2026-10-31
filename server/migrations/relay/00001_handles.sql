-- +goose Up
-- A push relay's only state: the push tokens apps have given it, each behind an
-- opaque handle that is all a Flare server ever sees.

-- Only the SHA-256 of a handle is kept, so a copy of this database does not
-- let anyone wake a device through the relay. The token must be kept as it is:
-- it is what the push service needs.
CREATE TABLE handles (
    handle_hash TEXT    PRIMARY KEY,
    secret_hash TEXT    NOT NULL,                      -- SHA-256 of the secret that lets the app remove the handle
    app_id      TEXT    NOT NULL,
    platform    TEXT    NOT NULL,
    provider    TEXT    NOT NULL CHECK (provider IN ('fcm', 'apns')),
    token       TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    woken_at    INTEGER                                -- NULL: never woken
);
-- A push token reaches one device, so registering it again replaces the handle.
CREATE UNIQUE INDEX handles_token ON handles (provider, app_id, token);

-- +goose Down
DROP TABLE handles;
