-- +goose Up
-- Messages are kept only for the cache duration. Subscribers keep their own
-- history; this table lets a client that was offline catch up.
CREATE TABLE messages (
    id         TEXT    PRIMARY KEY,           -- UUIDv7: sorts by creation time
    topic      TEXT    NOT NULL,
    time       INTEGER NOT NULL,              -- unix seconds
    expires_at INTEGER NOT NULL,              -- unix seconds
    priority   INTEGER NOT NULL DEFAULT 3,
    title      TEXT    NOT NULL DEFAULT '',
    body       TEXT    NOT NULL DEFAULT '',
    tags       TEXT    NOT NULL DEFAULT '[]', -- JSON array of strings
    click      TEXT    NOT NULL DEFAULT ''
);

-- Catching up on a topic: "messages after this ID", in order.
CREATE INDEX messages_topic_id ON messages (topic, id);
-- Purging expired messages.
CREATE INDEX messages_expires_at ON messages (expires_at);

-- +goose Down
DROP TABLE messages;
