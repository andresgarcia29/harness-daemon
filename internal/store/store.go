// Package store es el almacén: SQLite con las tres leyes del esquema
// (001_init.sql). Todo lo que entra pasa por UPSERTs idempotentes — repetir la
// ingesta entera no puede corromper nada, y eso es lo que convierte
// "at-least-once" en el cable en "exactly-once" en el almacén.
//
// Driver: modernc.org/sqlite (Go puro). CGO_ENABLED=0 sigue cross-compilando
// darwin/linux × arm64/amd64 — un driver con C nos costaría la matriz entera.
package store

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	DB *sql.DB
}

// Open abre (o crea) la base y aplica las migraciones pendientes.
// FORWARD-ONLY: antes de migrar, copia la DB a .bak-<n> — revertir el binario
// no puede costarte los datos (ley del esquema).
func Open(path string) (*Store, error) {
	// _pragma via DSN: cada conexión del pool las hereda.
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// WAL = 1 escritor + N lectores CONCURRENTES. Con una sola conexión, una
	// lectura del API se encolaba detrás de las escrituras del colector y el
	// panel se colgaba mientras ingería. Varias conexiones dejan que las
	// lecturas (snapshot, /api/session) corran mientras el colector escribe; las
	// escrituras siguen serializadas por la app (un Tick a la vez) + busy_timeout.
	db.SetMaxOpenConns(4)
	s := &Store{DB: db}
	if err := s.migrate(path); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) schemaVersion() int {
	var v string
	err := s.DB.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
	if err != nil {
		return 0 // sin tabla meta = base virgen
	}
	n, _ := strconv.Atoi(v)
	return n
}

func (s *Store) migrate(dbPath string) error {
	entries, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries) // 001_, 002_, … el orden ES el contrato
	current := s.schemaVersion()
	for _, name := range entries {
		base := strings.TrimPrefix(name, "migrations/")
		n, err := strconv.Atoi(base[:3])
		if err != nil {
			return fmt.Errorf("migración con nombre inválido %q (debe empezar NNN_)", base)
		}
		if n <= current {
			continue
		}
		if current > 0 && dbPath != ":memory:" {
			// respaldo ANTES de tocar nada; si ya existe, no lo pisamos
			bak := dbPath + ".bak-" + base[:3]
			if _, err := os.Stat(bak); os.IsNotExist(err) {
				if b, err := os.ReadFile(dbPath); err == nil {
					_ = os.WriteFile(bak, b, 0o600)
				}
			}
		}
		sqlText, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := s.DB.Exec(string(sqlText)); err != nil {
			return fmt.Errorf("migración %s: %w", base, err)
		}
		if _, err := s.DB.Exec(
			`INSERT INTO meta(key,value) VALUES('schema_version',?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(n)); err != nil {
			return err
		}
		current = n
	}
	return nil
}

// ── Identidad ────────────────────────────────────────────────────────────

func (s *Store) UpsertMachine(id, hostname, osName, arch, kind string, now int64) error {
	_, err := s.DB.Exec(`
		INSERT INTO machines (id, hostname, os, arch, kind, first_seen, last_seen)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  hostname=excluded.hostname, kind=excluded.kind, last_seen=excluded.last_seen`,
		id, hostname, osName, arch, kind, now, now)
	return err
}

func (s *Store) UpsertWorkspace(id, remote, name string, now int64) error {
	_, err := s.DB.Exec(`
		INSERT INTO workspaces (id, remote, name, first_seen, last_seen)
		VALUES (?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET last_seen=excluded.last_seen`,
		id, remote, name, now, now)
	return err
}

func (s *Store) UpsertWorkspacePath(machineID, workspaceID, path string, now int64) error {
	_, err := s.DB.Exec(`
		INSERT INTO workspace_paths (machine_id, workspace_id, path, last_seen)
		VALUES (?,?,?,?)
		ON CONFLICT(machine_id, path) DO UPDATE SET
		  workspace_id=excluded.workspace_id, last_seen=excluded.last_seen`,
		machineID, workspaceID, path, now)
	return err
}

func (s *Store) UpsertSession(id, machineID, workspaceID, cli string, started, now int64) error {
	_, err := s.DB.Exec(`
		INSERT INTO sessions (id, machine_id, workspace_id, cli, started, last_seen)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  last_seen=MAX(sessions.last_seen, excluded.last_seen)`,
		id, machineID, workspaceID, cli, started, now)
	return err
}

func (s *Store) UpsertAgent(sessionID, agentID, parent, typ, desc string, depth int, started, now int64) error {
	var parentVal any
	if parent != "" {
		parentVal = parent
	}
	_, err := s.DB.Exec(`
		INSERT INTO agents (session_id, id, parent_agent_id, type, description, spawn_depth, started, last_seen)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id, id) DO UPDATE SET
		  parent_agent_id=COALESCE(excluded.parent_agent_id, agents.parent_agent_id),
		  type=COALESCE(NULLIF(excluded.type,''), agents.type),
		  description=COALESCE(NULLIF(excluded.description,''), agents.description),
		  last_seen=MAX(agents.last_seen, excluded.last_seen)`,
		sessionID, agentID, parentVal, typ, desc, depth, started, now)
	return err
}

// ── Hechos medidos ───────────────────────────────────────────────────────

// Call es una llamada a la API tal como se OBSERVÓ (el modelo sin normalizar).
type Call struct {
	MessageID    string
	RequestID    string
	SessionID    string
	AgentID      string
	Model        string
	Speed        string
	In, Out      int64
	CacheRead    int64
	CacheWrite5m int64
	CacheWrite1h int64
	TS           int64
}

// UpsertCall — message_id es PK: contar dos veces la misma llamada es
// IMPOSIBLE, no "improbable". Los records repetidos del mismo message.id
// traen el usage acumulándose: nos quedamos con el MÁXIMO de cada campo
// (medido en transcripts reales: los repetidos nunca decrecen).
func (s *Store) UpsertCall(c Call) error {
	if c.Speed == "" {
		c.Speed = "standard"
	}
	_, err := s.DB.Exec(`
		INSERT INTO calls (message_id, request_id, session_id, agent_id, model, speed,
		                   input_tokens, output_tokens, cache_read_tokens,
		                   cache_write_5m_tokens, cache_write_1h_tokens, ts)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(message_id) DO UPDATE SET
		  input_tokens          = MAX(calls.input_tokens,          excluded.input_tokens),
		  output_tokens         = MAX(calls.output_tokens,         excluded.output_tokens),
		  cache_read_tokens     = MAX(calls.cache_read_tokens,     excluded.cache_read_tokens),
		  cache_write_5m_tokens = MAX(calls.cache_write_5m_tokens, excluded.cache_write_5m_tokens),
		  cache_write_1h_tokens = MAX(calls.cache_write_1h_tokens, excluded.cache_write_1h_tokens)`,
		c.MessageID, c.RequestID, c.SessionID, c.AgentID, c.Model, c.Speed,
		c.In, c.Out, c.CacheRead, c.CacheWrite5m, c.CacheWrite1h, c.TS)
	return err
}

// Event es una línea del bus del harness.
type Event struct {
	UID         string
	TS          int64
	MachineID   string
	WorkspaceID string
	SessionID   string
	TaskID      string
	Kind        string
	Actor       string
	Summary     string
	OK          *bool // nil = no aplica
}

// EventUID: hash(archivo + offset). Releer la misma línea produce el mismo
// uid — la ingesta del bus es idempotente sin pedirle nada a bash.
func EventUID(source string, offset int64) string {
	h := sha256.Sum256([]byte(source + "#" + strconv.FormatInt(offset, 10)))
	return hex.EncodeToString(h[:16])
}

func (s *Store) InsertEvent(e Event) error {
	var ok any
	if e.OK != nil {
		if *e.OK {
			ok = 1
		} else {
			ok = 0
		}
	}
	_, err := s.DB.Exec(`
		INSERT INTO events (uid, ts, machine_id, workspace_id, session_id, task_id, kind, actor, summary, ok)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO NOTHING`,
		e.UID, e.TS, e.MachineID, e.WorkspaceID, e.SessionID, e.TaskID, e.Kind, e.Actor, e.Summary, ok)
	return err
}

func (s *Store) UpsertTask(workspaceID, id, title, origin, phase string, now int64) error {
	_, err := s.DB.Exec(`
		INSERT INTO tasks (workspace_id, id, title, origin, phase, started, last_seen)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(workspace_id, id) DO UPDATE SET
		  title=COALESCE(NULLIF(excluded.title,''), tasks.title),
		  origin=COALESCE(NULLIF(excluded.origin,''), tasks.origin),
		  phase=COALESCE(NULLIF(excluded.phase,''), tasks.phase),
		  last_seen=MAX(tasks.last_seen, excluded.last_seen)`,
		workspaceID, id, title, origin, phase, now, now)
	return err
}

// ── Offsets de tailing ───────────────────────────────────────────────────

type Offset struct {
	Dev, Ino uint64
	Offset   int64
}

func (s *Store) GetOffset(source string) (Offset, error) {
	var o Offset
	err := s.DB.QueryRow(`SELECT dev, ino, offset FROM offsets WHERE source=?`, source).
		Scan(&o.Dev, &o.Ino, &o.Offset)
	if err == sql.ErrNoRows {
		return Offset{}, nil
	}
	return o, err
}

func (s *Store) SetOffset(source string, o Offset, now int64) error {
	_, err := s.DB.Exec(`
		INSERT INTO offsets (source, dev, ino, offset, last_read)
		VALUES (?,?,?,?,?)
		ON CONFLICT(source) DO UPDATE SET
		  dev=excluded.dev, ino=excluded.ino, offset=excluded.offset, last_read=excluded.last_read`,
		source, o.Dev, o.Ino, o.Offset, now)
	return err
}

// Counts — cuántas filas hay por tabla. Para /api/stats: la prueba de vida
// del colector es que estos números crecen.
func (s *Store) Counts() (map[string]int64, error) {
	out := map[string]int64{}
	for _, t := range []string{"machines", "workspaces", "sessions", "agents", "calls", "events", "tasks"} {
		var n int64
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}

// ── Hilo de razonamiento (migración 002) ─────────────────────────────────

// ThreadItem es un bloque del hilo de un agente.
type ThreadItem struct {
	Seq  int64
	TS   int64
	Kind string // text | think | tool
	Text string
	Hint string
}

// UpsertThread guarda un bloque; idempotente por (session, agent, seq).
func (s *Store) UpsertThread(sessionID, agentID string, it ThreadItem) error {
	var ts any
	if it.TS > 0 {
		ts = it.TS
	}
	_, err := s.DB.Exec(`
		INSERT INTO agent_thread (session_id, agent_id, seq, ts, kind, text, hint)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(session_id, agent_id, seq) DO NOTHING`,
		sessionID, agentID, it.Seq, ts, it.Kind, it.Text, it.Hint)
	return err
}

// AgentThread devuelve el hilo de un agente en orden (acotado a los últimos N).
func (s *Store) AgentThread(sessionID, agentID string, limit int) ([]ThreadItem, error) {
	rows, err := s.DB.Query(`
		SELECT seq, COALESCE(ts,0), kind, COALESCE(text,''), COALESCE(hint,'')
		FROM agent_thread WHERE session_id = ? AND agent_id = ?
		ORDER BY seq DESC LIMIT ?`, sessionID, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ThreadItem
	for rows.Next() {
		var it ThreadItem
		if rows.Scan(&it.Seq, &it.TS, &it.Kind, &it.Text, &it.Hint) == nil {
			out = append(out, it)
		}
	}
	// se pidió DESC para acotar a los últimos N; el hilo se lee ASC
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
