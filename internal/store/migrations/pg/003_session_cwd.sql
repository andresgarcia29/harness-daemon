-- 003_session_cwd.sql (Postgres) — igual que migrations/003_session_cwd.sql:
-- ancla la sesión a un directorio real para cruzarla con las terminales de herdr.
ALTER TABLE sessions ADD COLUMN cwd TEXT NOT NULL DEFAULT '';
