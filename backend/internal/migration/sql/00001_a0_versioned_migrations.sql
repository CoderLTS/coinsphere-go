-- +goose Up
-- A0 establishes migration history without replacing the existing GORM-managed schema.
SELECT 1;

-- +goose Down
SELECT 1;
