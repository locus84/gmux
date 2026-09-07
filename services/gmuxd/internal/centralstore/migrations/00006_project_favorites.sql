-- +goose Up
ALTER TABLE project_entries
ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0 CHECK (favorite IN (0, 1));

-- +goose Down
ALTER TABLE project_entries DROP COLUMN favorite;
