-- +goose Up
-- Who may use the server: users, the access tokens they log in with, and the
-- rules that say which topics they may read and write.

CREATE TABLE users (
    id            TEXT    PRIMARY KEY,
    name          TEXT    NOT NULL UNIQUE,             -- lower case
    password_hash TEXT    NOT NULL,                    -- Argon2id, with its own salt
    role          TEXT    NOT NULL CHECK (role IN ('user', 'admin')),
    created_at    INTEGER NOT NULL                     -- unix seconds
);

-- Only the SHA-256 of a token is stored, so a copy of the database yields no
-- usable token. The id is what people see when they list or revoke tokens.
CREATE TABLE tokens (
    id           TEXT    PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   TEXT    NOT NULL UNIQUE,
    label        TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER,                              -- NULL: never expires
    last_used_at INTEGER                               -- NULL: never used
);
CREATE INDEX tokens_user_id ON tokens (user_id);

-- Access rules. A pattern is a topic name where * stands for any run of
-- characters. user_id NULL is a rule for everyone.
CREATE TABLE acl (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT    REFERENCES users (id) ON DELETE CASCADE,
    pattern TEXT    NOT NULL,
    access  TEXT    NOT NULL CHECK (access IN ('read-write', 'read-only', 'write-only', 'deny-all'))
);
-- One rule per user and pattern. (NULLs are all distinct in a plain unique
-- index, so "everyone" is spelled out.)
CREATE UNIQUE INDEX acl_user_pattern ON acl (COALESCE(user_id, ''), pattern);

-- +goose Down
DROP TABLE acl;
DROP TABLE tokens;
DROP TABLE users;
