-- +goose Up
-- Push notifications: the devices that want them, the topics each follows, and
-- the queue of notifications waiting to be sent.

-- A device is one app installation that can be woken by a push service. target
-- is what that service needs to reach it: an FCM registration token, an APNs
-- device token, a Web Push or UnifiedPush endpoint, or a relay's handle.
-- A device with no user is an anonymous visitor's, and acts with their access.
CREATE TABLE devices (
    id          TEXT    PRIMARY KEY,
    secret_hash TEXT    NOT NULL,                      -- SHA-256 of the secret the app was given
    user_id     TEXT    REFERENCES users (id) ON DELETE CASCADE,
    app_id      TEXT    NOT NULL,
    platform    TEXT    NOT NULL,
    provider    TEXT    NOT NULL CHECK (provider IN ('fcm', 'apns', 'webpush', 'unifiedpush', 'relay')),
    target      TEXT    NOT NULL,
    p256dh      TEXT    NOT NULL DEFAULT '',           -- Web Push: the browser's public key
    auth_secret TEXT    NOT NULL DEFAULT '',           -- Web Push: the shared authentication secret
    relay_url   TEXT    NOT NULL DEFAULT '',
    app_version TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    seen_at     INTEGER NOT NULL
);
-- A push token reaches one device, so it is registered once: registering it
-- again replaces the earlier registration instead of sending everything twice.
CREATE UNIQUE INDEX devices_target ON devices (provider, relay_url, target);
CREATE INDEX devices_user_id ON devices (user_id);

-- The topics a device is woken for. The id is opaque and goes into every push,
-- so that the app can tell which subscription it belongs to without the push
-- service learning the topic.
CREATE TABLE push_subscriptions (
    id         TEXT    PRIMARY KEY,
    device_id  TEXT    NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    topic      TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (device_id, topic)
);
CREATE INDEX push_subscriptions_topic ON push_subscriptions (topic);

-- One row per notification still to be sent. A row is added in the same
-- transaction as the message it is for, so a stored message is never left
-- without its notifications. next_attempt_at is when to try next; a worker that
-- takes a row moves it forward, so that a crash only delays the notification.
CREATE TABLE push_jobs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id       TEXT    NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    subscription_id TEXT    NOT NULL REFERENCES push_subscriptions (id) ON DELETE CASCADE,
    message_id      TEXT    NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,                  -- unix seconds
    expires_at      INTEGER NOT NULL                   -- the message's expiry: give up then
);
CREATE INDEX push_jobs_due ON push_jobs (next_attempt_at);
CREATE INDEX push_jobs_expires_at ON push_jobs (expires_at);

-- Small values the server keeps for itself, such as its Web Push signing key.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE settings;
DROP TABLE push_jobs;
DROP TABLE push_subscriptions;
DROP TABLE devices;
