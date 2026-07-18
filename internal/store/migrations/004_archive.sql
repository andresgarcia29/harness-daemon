-- archived: ocultar de las listas del panel sesiones/tareas viejas ("basura")
-- SIN borrar los archivos del usuario (transcripts de Claude, dirs de tarea).
-- El colector hace UPSERT que no toca esta columna, así que archivar PERSISTE
-- aunque el archivo siga existiendo y se re-escanee. Reversible.
ALTER TABLE sessions ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
