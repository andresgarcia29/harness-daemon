-- 004_archive.sql (Postgres) — igual que migrations/004_archive.sql:
-- archivar (ocultar del panel) sin borrar los archivos del usuario. Reversible.
ALTER TABLE sessions ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
