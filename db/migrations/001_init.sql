-- 001_init.sql — el esquema. Lo ÚNICO caro de cambiar del proyecto.
--
-- Binarios, frameworks y vistas se tiran y se rehacen en una tarde. Los ids y
-- las relaciones son para siempre: cambiarlos invalida los datos históricos de
-- todas las máquinas. Por eso este archivo se pensó antes de teclear nada.
--
-- Tres leyes que rigen todo lo de abajo:
--   1. GUARDA LO QUE MEDISTE, CALCULA LO QUE INFERISTE. Los tokens son un
--      hecho → columna. El dinero es una interpretación → VISTA. Así, cuando
--      mañana agreguemos el precio de GLM, TODO el histórico se re-cotiza solo.
--   2. LA DEDUPLICACIÓN ES UNA RESTRICCIÓN, NO UNA CONVENCIÓN. message_id es
--      PRIMARY KEY: contar dos veces la misma llamada es imposible, no
--      "improbable si el código está bien". Y como el UPSERT es idempotente,
--      la ingesta se puede repetir entera sin corromper nada — que es lo que
--      convierte "at-least-once" en el cable en "exactly-once" en el almacén.
--   3. LA IDENTIDAD NO ES LA RUTA. El mismo repo es /Users/… en el Mac y
--      /home/… en el VPC. Si la clave fuera la ruta, serían dos workspaces y
--      no centralizarías nada.
--
-- Migraciones: FORWARD-ONLY y aditivas (se agregan columnas, no se borran).
-- Antes de migrar, el daemon copia la DB a harness.db.bak-<version>: revertir
-- el binario no puede costarte los datos.

PRAGMA journal_mode = WAL;      -- lectores concurrentes mientras el daemon escribe
PRAGMA foreign_keys = ON;

-- ── Identidad ────────────────────────────────────────────────────────────

-- Una máquina. El id es un UUID generado al primer arranque
-- (~/.config/harness/machine-id). NUNCA el hostname: colisiona entre clientes
-- ("macbook-pro") y cambia cuando alguien renombra su laptop.
CREATE TABLE machines (
  id          TEXT PRIMARY KEY,
  hostname    TEXT,
  os          TEXT,             -- darwin | linux
  arch        TEXT,             -- arm64 | amd64
  kind        TEXT,             -- laptop | vpc | k8s-cronjob | ci
  first_seen  INTEGER NOT NULL,
  last_seen   INTEGER NOT NULL
);

-- Un workspace: una instancia del harness. La identidad es el REMOTE de git
-- normalizado (github.com/corvux/atlas), no la ruta local. Así "COR-42" es la
-- misma tarea la corras en tu Mac o en el VPC — que es todo el punto de
-- centralizar. Sin remote (git init local) → "local:<machine_id>:<ruta>", que
-- es honesto: ese workspace ES local a esa máquina y no se puede unificar.
CREATE TABLE workspaces (
  id          TEXT PRIMARY KEY,
  remote      TEXT,
  name        TEXT,
  first_seen  INTEGER NOT NULL,
  last_seen   INTEGER NOT NULL
);

-- Dónde vive ese workspace EN CADA máquina. La ruta es un hecho local, no la
-- identidad: por eso vive en su propia tabla y no como columna de workspaces.
CREATE TABLE workspace_paths (
  machine_id    TEXT NOT NULL REFERENCES machines(id),
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id),
  path          TEXT NOT NULL,
  last_seen     INTEGER NOT NULL,
  PRIMARY KEY (machine_id, path)
);

-- Una sesión = un proceso de agente (una terminal). Diez sesiones abiertas en
-- el mismo repo son diez filas: es lo que hace posible atribuir sin mezclar.
CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,     -- session_id que da el CLI
  machine_id    TEXT NOT NULL REFERENCES machines(id),
  workspace_id  TEXT REFERENCES workspaces(id),
  cli           TEXT,                 -- claude-code | codex | gemini | ...
  cli_version   TEXT,
  git_branch    TEXT,
  started       INTEGER NOT NULL,
  last_seen     INTEGER NOT NULL
);

-- Un turno = un prompt tuyo. prompt_id es el trace_id que el CLI ya emite en
-- hooks, statusline y OTel: correlaciona "este prompt lanzó estos agentes y
-- costó esto" sin que inventemos nada.
CREATE TABLE turns (
  id          TEXT PRIMARY KEY,       -- prompt_id
  session_id  TEXT NOT NULL REFERENCES sessions(id),
  started     INTEGER NOT NULL,
  ended       INTEGER
);

-- Un agente: 'main' (el orquestador) o un subagente. La PK es compuesta porque
-- agent_id es único POR SESIÓN, no globalmente.
CREATE TABLE agents (
  session_id      TEXT NOT NULL REFERENCES sessions(id),
  id              TEXT NOT NULL,
  turn_id         TEXT REFERENCES turns(id),
  parent_agent_id TEXT,               -- NULL = main. Sale de meta.json, no se infiere
  type            TEXT,               -- general-purpose | reviewer | implementer | ...
  description     TEXT,               -- ← la IDENTIDAD legible. "general-purpose" no identifica a nadie
  spawn_depth     INTEGER NOT NULL DEFAULT 0,
  started         INTEGER NOT NULL,
  last_seen       INTEGER NOT NULL,
  PRIMARY KEY (session_id, id)
);

-- ── Hechos medidos ───────────────────────────────────────────────────────

-- Una llamada a la API. message_id es PK: ver ley 2 arriba.
-- El modelo se guarda TAL CUAL SE OBSERVÓ. Normalizar destructivamente aquí
-- (mapear "glm-4.6" a "unknown") destruye información que mañana vale.
CREATE TABLE calls (
  message_id            TEXT PRIMARY KEY,
  request_id            TEXT,
  session_id            TEXT NOT NULL REFERENCES sessions(id),
  agent_id              TEXT NOT NULL,
  turn_id               TEXT,
  model                 TEXT NOT NULL,
  speed                 TEXT NOT NULL DEFAULT 'standard',   -- standard | fast (SKU distinto)
  input_tokens          INTEGER NOT NULL DEFAULT 0,
  output_tokens         INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
  cache_write_5m_tokens INTEGER NOT NULL DEFAULT 0,          -- 1.25× — se factura distinto
  cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0,          -- 2.00× — por eso NO se suman juntos
  ts                    INTEGER NOT NULL,
  FOREIGN KEY (session_id, agent_id) REFERENCES agents(session_id, id)
);
CREATE INDEX calls_ts       ON calls(ts);
CREATE INDEX calls_session  ON calls(session_id, ts);
CREATE INDEX calls_model    ON calls(model);

-- El bus del harness: fases, gates, decisiones, supuestos, paradas.
-- Esto es NUESTRO (lo escriben ship.sh, /auto y los hooks), no de ningún CLI:
-- funciona igual con Claude Code, Codex o un humano. Es la capa universal.
-- uid = hash(archivo + offset): releer la misma línea produce el mismo uid, así
-- que la ingesta es idempotente sin pedirle nada a bash.
CREATE TABLE events (
  uid           TEXT PRIMARY KEY,
  ts            INTEGER NOT NULL,
  machine_id    TEXT NOT NULL REFERENCES machines(id),
  workspace_id  TEXT REFERENCES workspaces(id),
  session_id    TEXT,
  task_id       TEXT,
  kind          TEXT NOT NULL,   -- gate | phase | decision | assumption | stop | tool | deploy
  actor         TEXT,
  summary       TEXT,
  ok            INTEGER          -- 1 pasó · 0 bloqueó · NULL n/a. Un gate que BLOQUEÓ
);                               -- es la mejor prueba de que el harness es real: se ve igual que un éxito
CREATE INDEX events_ts   ON events(ts);
CREATE INDEX events_task ON events(workspace_id, task_id, ts);

CREATE TABLE tasks (
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id),
  id            TEXT NOT NULL,
  title         TEXT,
  origin        TEXT,            -- ticket | prompt
  phase         TEXT,            -- intake | rfc | implement | review | ship | deploy | archive
  assumptions   INTEGER NOT NULL DEFAULT 0,
  started       INTEGER NOT NULL,
  last_seen     INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

-- ── Interpretación ───────────────────────────────────────────────────────

-- Precios. Un modelo SIN precio no cuesta 0 ni cuesta "lo que Opus": cuesta
-- NULL = desconocido, y la UI enseña "—". Inventar un número con dos decimales
-- es peor que no dar ninguno: parece un hecho.
CREATE TABLE prices (
  model           TEXT NOT NULL,
  speed           TEXT NOT NULL DEFAULT 'standard',
  valid_from      INTEGER NOT NULL DEFAULT 0,   -- histórico: los precios cambian
  provider        TEXT,
  input           REAL,     -- USD por millón de tokens
  output          REAL,
  cache_read      REAL,
  cache_write_5m  REAL,
  cache_write_1h  REAL,
  source          TEXT,     -- builtin | litellm | user
  PRIMARY KEY (model, speed, valid_from)
);

-- EL COSTO ES UNA VISTA. No una columna. Agregar el precio de un modelo nuevo
-- re-cotiza el histórico entero sin tocar un solo dato medido.
CREATE VIEW call_costs AS
SELECT
  c.*,
  p.provider,
  (p.input IS NOT NULL) AS priced,
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

-- ── Mecánica del colector ────────────────────────────────────────────────

-- Offsets de tailing. (dev, ino) detecta rotación — dev importa: los inodos
-- solo son únicos por dispositivo.
-- acked_offset es el buffer del modo cloud: no avanza hasta que el servidor
-- confirma. Con eso, el buffer ES el propio JSONL — no hay cola que mantener,
-- y tu laptop en un avión no pierde nada.
CREATE TABLE offsets (
  source        TEXT PRIMARY KEY,
  dev           INTEGER,
  ino           INTEGER,
  offset        INTEGER NOT NULL DEFAULT 0,
  acked_offset  INTEGER NOT NULL DEFAULT 0,
  last_read     INTEGER
);

CREATE TABLE meta (
  key    TEXT PRIMARY KEY,
  value  TEXT
);
INSERT INTO meta (key, value) VALUES ('schema_version', '1');
