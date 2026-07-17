-- 002_thread.sql — el hilo de razonamiento por agente (texto, pensamiento,
-- herramientas). Es lo que el humano pidió leer: "qué razonó, por qué tardó".
--
-- Forward-only y aditiva (no toca 001). La PK (session_id, agent_id, seq) hace
-- la ingesta idempotente: seq = offset del byte en el transcript × 100 + índice
-- de bloque, único por posición en el archivo. Releer la misma línea produce el
-- mismo seq → ON CONFLICT DO NOTHING, sin duplicar.
--
-- El texto se guarda YA REDACTADO por el colector — la ley de secretos también
-- aplica a lo que se persiste, no solo a lo que se muestra.

CREATE TABLE agent_thread (
  session_id  TEXT NOT NULL,
  agent_id    TEXT NOT NULL,
  seq         INTEGER NOT NULL,   -- orden estable dentro del transcript
  ts          INTEGER,            -- reloj del record, o NULL si no traía
  kind        TEXT NOT NULL,      -- text | think | tool
  text        TEXT,               -- el contenido (redactado, recortado)
  hint        TEXT,               -- para tool: la pista de input (ruta/comando)
  PRIMARY KEY (session_id, agent_id, seq)
);
CREATE INDEX agent_thread_agent ON agent_thread(session_id, agent_id, seq);
