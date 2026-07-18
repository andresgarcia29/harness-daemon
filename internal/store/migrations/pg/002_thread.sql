-- 002_thread.sql (Postgres) — igual que migrations/002_thread.sql (SQLite):
-- el hilo de razonamiento por agente. INTEGER → BIGINT (seq = offset×100 + bloque
-- puede pasar los 32 bits). Forward-only y aditiva.
CREATE TABLE agent_thread (
  session_id  TEXT NOT NULL,
  agent_id    TEXT NOT NULL,
  seq         BIGINT NOT NULL,
  ts          BIGINT,
  kind        TEXT NOT NULL,
  text        TEXT,
  hint        TEXT,
  PRIMARY KEY (session_id, agent_id, seq)
);
CREATE INDEX agent_thread_agent ON agent_thread(session_id, agent_id, seq);
