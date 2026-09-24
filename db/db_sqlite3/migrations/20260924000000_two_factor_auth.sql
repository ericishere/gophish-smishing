-- +goose Up
ALTER TABLE users ADD COLUMN two_factor_required BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_blocked_until BIGINT NOT NULL DEFAULT 0;
CREATE TABLE two_factor_methods (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id BIGINT NOT NULL,
    name VARCHAR(64) NOT NULL,
    secret VARCHAR(128) NOT NULL,
    last_used_step BIGINT NOT NULL DEFAULT -1,
    created_at DATETIME NOT NULL
);
CREATE INDEX two_factor_methods_user ON two_factor_methods (user_id);
CREATE TABLE two_factor_challenges (
    id VARCHAR(64) PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE,
    version BIGINT NOT NULL,
    password_fingerprint VARCHAR(64) NOT NULL,
    purpose VARCHAR(16) NOT NULL,
    secret VARCHAR(128) NOT NULL,
    expires_at BIGINT NOT NULL
);

-- +goose Down
DROP TABLE two_factor_challenges;
DROP TABLE two_factor_methods;
-- Rebuild for SQLite versions predating DROP COLUMN support.
CREATE TABLE users_without_two_factor (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username VARCHAR(255) NOT NULL UNIQUE,
    hash VARCHAR(255),
    api_key VARCHAR(255) NOT NULL UNIQUE,
    role_id INTEGER,
    password_change_required BOOLEAN,
    last_login DATETIME,
    account_locked BOOLEAN
);
INSERT INTO users_without_two_factor (id, username, hash, api_key, role_id, password_change_required, last_login, account_locked)
SELECT id, username, hash, api_key, role_id, password_change_required, last_login, account_locked FROM users;
DROP TABLE users;
ALTER TABLE users_without_two_factor RENAME TO users;
