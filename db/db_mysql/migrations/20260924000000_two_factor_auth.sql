-- +goose Up
ALTER TABLE users ADD COLUMN two_factor_required BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_blocked_until BIGINT NOT NULL DEFAULT 0;
CREATE TABLE two_factor_methods (
    id INTEGER PRIMARY KEY AUTO_INCREMENT,
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
ALTER TABLE users DROP COLUMN two_factor_blocked_until;
ALTER TABLE users DROP COLUMN two_factor_failures;
ALTER TABLE users DROP COLUMN two_factor_version;
ALTER TABLE users DROP COLUMN two_factor_required;
