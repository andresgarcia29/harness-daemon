-- cwd de la sesión, tomado del campo `cwd` del transcript de Claude Code.
-- Ancla la sesión a un directorio REAL para cruzarla con las terminales vivas
-- de herdr (pane.cwd) y saber si de verdad está corriendo AHORA — en vez de
-- adivinar por el mtime del archivo (que miente: marca "activa" algo que ya
-- terminó, o nada). Es la base para una liveness honesta.
ALTER TABLE sessions ADD COLUMN cwd TEXT NOT NULL DEFAULT '';
