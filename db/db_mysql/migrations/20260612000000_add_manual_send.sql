-- +goose Up
-- SQL in section 'Up' is executed when this migration is applied
ALTER TABLE campaigns ADD COLUMN manual_send TINYINT(1) NOT NULL DEFAULT 0;

-- +goose Down
-- SQL section 'Down' is executed when this migration is rolled back
ALTER TABLE campaigns DROP COLUMN manual_send;