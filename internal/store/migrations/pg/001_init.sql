-- 001_init.sql (Postgres) — el MISMO esquema y las MISMAS tres leyes que
-- migrations/001_init.sql (SQLite). Sólo cambia el dialecto:
--   · sin PRAGMA (son de SQLite; el pool PG se configura en Go).
--   · INTEGER → BIGINT — los timestamps en ms y los tokens desbordan el INTEGER
--     de 32 bits de Postgres (en SQLite INTEGER ya es de 64).
--   · REAL → DOUBLE PRECISION — el REAL de Postgres es de 4 bytes; el de SQLite
--     de 8. Los precios quieren la precisión de 8.
--   · "offset" entre comillas — es palabra reservada en Postgres.
--   · priced en la vista como ENTERO (CASE … 1/0), no boolean: el Go hace
--     SUM(priced) y SUM(1 - priced).
-- Las relaciones, los ids y los UPSERTs idempotentes son idénticos: los datos
-- históricos son los mismos los guarde SQLite o Postgres.

CREATE TABLE machines (
  id          TEXT PRIMARY KEY,
  hostname    TEXT,
  os          TEXT,
  arch        TEXT,
  kind        TEXT,
  first_seen  BIGINT NOT NULL,
  last_seen   BIGINT NOT NULL
);

CREATE TABLE workspaces (
  id          TEXT PRIMARY KEY,
  remote      TEXT,
  name        TEXT,
  first_seen  BIGINT NOT NULL,
  last_seen   BIGINT NOT NULL
);

CREATE TABLE workspace_paths (
  machine_id    TEXT NOT NULL REFERENCES machines(id),
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id),
  path          TEXT NOT NULL,
  last_seen     BIGINT NOT NULL,
  PRIMARY KEY (machine_id, path)
);

CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,
  machine_id    TEXT NOT NULL REFERENCES machines(id),
  workspace_id  TEXT REFERENCES workspaces(id),
  cli           TEXT,
  cli_version   TEXT,
  git_branch    TEXT,
  started       BIGINT NOT NULL,
  last_seen     BIGINT NOT NULL
);

CREATE TABLE turns (
  id          TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL REFERENCES sessions(id),
  started     BIGINT NOT NULL,
  ended       BIGINT
);

CREATE TABLE agents (
  session_id      TEXT NOT NULL REFERENCES sessions(id),
  id              TEXT NOT NULL,
  turn_id         TEXT REFERENCES turns(id),
  parent_agent_id TEXT,
  type            TEXT,
  description     TEXT,
  spawn_depth     BIGINT NOT NULL DEFAULT 0,
  started         BIGINT NOT NULL,
  last_seen       BIGINT NOT NULL,
  PRIMARY KEY (session_id, id)
);

CREATE TABLE calls (
  message_id            TEXT PRIMARY KEY,
  request_id            TEXT,
  session_id            TEXT NOT NULL REFERENCES sessions(id),
  agent_id              TEXT NOT NULL,
  turn_id               TEXT,
  model                 TEXT NOT NULL,
  speed                 TEXT NOT NULL DEFAULT 'standard',
  input_tokens          BIGINT NOT NULL DEFAULT 0,
  output_tokens         BIGINT NOT NULL DEFAULT 0,
  cache_read_tokens     BIGINT NOT NULL DEFAULT 0,
  cache_write_5m_tokens BIGINT NOT NULL DEFAULT 0,
  cache_write_1h_tokens BIGINT NOT NULL DEFAULT 0,
  ts                    BIGINT NOT NULL,
  FOREIGN KEY (session_id, agent_id) REFERENCES agents(session_id, id)
);
CREATE INDEX calls_ts       ON calls(ts);
CREATE INDEX calls_session  ON calls(session_id, ts);
CREATE INDEX calls_model    ON calls(model);

CREATE TABLE events (
  uid           TEXT PRIMARY KEY,
  ts            BIGINT NOT NULL,
  machine_id    TEXT NOT NULL REFERENCES machines(id),
  workspace_id  TEXT REFERENCES workspaces(id),
  session_id    TEXT,
  task_id       TEXT,
  kind          TEXT NOT NULL,
  actor         TEXT,
  summary       TEXT,
  ok            INTEGER
);
CREATE INDEX events_ts   ON events(ts);
CREATE INDEX events_task ON events(workspace_id, task_id, ts);

CREATE TABLE tasks (
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id),
  id            TEXT NOT NULL,
  title         TEXT,
  origin        TEXT,
  phase         TEXT,
  assumptions   BIGINT NOT NULL DEFAULT 0,
  started       BIGINT NOT NULL,
  last_seen     BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE prices (
  model           TEXT NOT NULL,
  speed           TEXT NOT NULL DEFAULT 'standard',
  valid_from      BIGINT NOT NULL DEFAULT 0,
  provider        TEXT,
  input           DOUBLE PRECISION,
  output          DOUBLE PRECISION,
  cache_read      DOUBLE PRECISION,
  cache_write_5m  DOUBLE PRECISION,
  cache_write_1h  DOUBLE PRECISION,
  source          TEXT,
  PRIMARY KEY (model, speed, valid_from)
);

-- EL COSTO ES UNA VISTA (ley 1). priced como ENTERO para que SUM(priced) y
-- SUM(1 - priced) del Go funcionen igual que en SQLite.
CREATE VIEW call_costs AS
SELECT
  c.*,
  p.provider,
  CASE WHEN p.input IS NOT NULL THEN 1 ELSE 0 END AS priced,
  CASE WHEN p.input IS NULL THEN NULL ELSE (
      c.input_tokens          * p.input
    + c.output_tokens         * p.output
    + c.cache_read_tokens     * COALESCE(p.cache_read, 0)
    + c.cache_write_5m_tokens * COALESCE(p.cache_write_5m, 0)
    + c.cache_write_1h_tokens * COALESCE(p.cache_write_1h, 0)
  ) / 1000000.0 END AS cost_usd
FROM calls c
LEFT JOIN prices p
  ON  p.model = c.model
  AND p.speed = c.speed
  AND p.valid_from = (
        SELECT MAX(p2.valid_from) FROM prices p2
        WHERE p2.model = c.model AND p2.speed = c.speed AND p2.valid_from <= c.ts
      );

CREATE TABLE offsets (
  source        TEXT PRIMARY KEY,
  dev           BIGINT,
  ino           BIGINT,
  "offset"      BIGINT NOT NULL DEFAULT 0,
  acked_offset  BIGINT NOT NULL DEFAULT 0,
  last_read     BIGINT
);

CREATE TABLE meta (
  key    TEXT PRIMARY KEY,
  value  TEXT
);
INSERT INTO meta (key, value) VALUES ('schema_version', '1');
