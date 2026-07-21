# harness-daemon

**Ver lo que hacen tus agentes, en tiempo real, sin leer una consola.**

Un binario que observa el trabajo de agentes de código —Claude Code, Codex, lo
que venga— y lo muestra en un tablero que se entiende de un vistazo: qué está
pasando, **si algo te está esperando**, qué decidió por ti, cuándo se frenó a sí
mismo, y cuánto llevas gastado.

> **Ley vigente:** el daemon observa y puede crear o reanudar trabajo, pero no
> aprueba ni publica. Operar crea tareas y sesiones; llegar a `main` sigue
> dependiendo de los comandos y gates del harness. Ver ADR-0010, que reemplaza
> parcialmente la formulación original de ADR-0009.

---

## El problema

Un harness es invisible. Su promesa —*los agentes proponen, los sistemas
deterministas verifican*— es una afirmación que hay que creerte hasta que
alguien la ve pasar. Y mientras tanto, la consola es una manguera: te pierdes,
preguntas "¿dónde vamos?", y vigilas *por si acaso* te pide algo.

Cuatro preguntas, y solo cuatro:

1. ¿Dónde vamos?
2. **¿Me está esperando algo?** ← la que duele
3. ¿Quién está corriendo?
4. ¿Cuánto llevo gastado?

Ninguna se responde viendo *más* del agente.

## La idea de fondo

**No mires al agente. Mira el trabajo.**

Un worktree con commits, un gate que salió con exit 3, una tarea que cambió de
fase, un supuesto declarado — nada de eso pregunta **quién** lo escribió. Da
igual si fue Claude Code, Codex o un humano. Por eso la capa que vale no
necesita adaptadores: **el harness ya lo escribe en archivos**.

Tres capas, y solo una es frágil:

| capa | qué ve | integración |
|---|---|---|
| **0 · universal** | procesos vivos, worktrees, estado de git, mtimes | ninguna — funciona con cualquier agente, hoy |
| **1 · el harness** | fases, gates, decisiones, supuestos, paradas | ninguna — es nuestro bus (`events.jsonl`) |
| **2 · por CLI** | tokens, costo, texto en vivo | un adaptador por CLI · **degrada solo** |

Si la capa 2 se rompe (los transcripts son un formato interno que cambia entre
versiones), pierdes los tokens. No pierdes las fases, ni los gates, ni las tareas.

## Estado actual

El binario incluye colector, almacenamiento, panel web, wizard de init y plano
de operación local. SQLite es el almacén predeterminado; PostgreSQL es opcional
cuando se configura una conexión. La versión exacta siempre sale de
`harness version`; este README no duplica un número que pueda quedar obsoleto.

```bash
go build -o bin/harnessd ./cmd/harnessd

./bin/harnessd ensure     # arranca si no hay ninguno (idempotente)
./bin/harnessd status     # quién tiene el puerto
./bin/harnessd stop
./bin/harnessd selftest   # lo corre el updater ANTES de cambiar el binario
```

## Cómo corre

El modo implementado es `all-in-one`: colector, almacén, API y panel en un solo
proceso local. `harness ui` e `init` usan por defecto `127.0.0.1:7180`; los
comandos legacy del daemon conservan `7718` por compatibilidad. `harness
status` y `stop` resuelven ambos mediante la configuración local.

El acceso a una máquina remota se hace mediante el flujo SSH/herdr del panel;
este README no presenta todavía `collect` y `serve` como modos públicos porque
el entrypoint actual no los ofrece como contrato de CLI.

## Las decisiones, y por qué

Todas en [`docs/adr/`](docs/adr/). Las que más se notan:

- **[ADR-0001](docs/adr/ADR-0001-colector-local-backend-intercambiable.md)** — el colector es local; la nube es una implementación de `Sink`, no un rediseño.
- **[ADR-0002](docs/adr/ADR-0002-modelo-de-identidad.md)** — `machine_id` es un UUID (no el hostname); el workspace se identifica por su **git remote**, no por su ruta. Si fuera la ruta, el mismo repo en tu Mac y en el VPC serían dos, y centralizar no centralizaría nada.
- **[ADR-0003](docs/adr/ADR-0003-jsonl-cable-sqlite-almacen.md)** — JSONL sigue siendo el cable y SQLite el default; la implementación posterior añadió PostgreSQL opcional.
- **[ADR-0004](docs/adr/ADR-0004-costo-es-una-vista.md)** — **guarda lo que mediste, calcula lo que inferiste.** Los tokens son columna; el dinero es una VISTA. Un modelo sin precio cuesta `NULL`, nunca "lo que Opus".
- **[ADR-0005](docs/adr/ADR-0005-singleton-por-puerto.md)** — **el puerto ES el lock.** Sin pidfile, sin ventana de adquisición, sin locks huérfanos.
- **[ADR-0006](docs/adr/ADR-0006-auto-update.md)** — diseño aceptado de update firmado; la ruta actual usa releases/Homebrew y la firma minisign sigue pendiente.
- **[ADR-0007](docs/adr/ADR-0007-que-no-sale-de-la-maquina.md)** — *si no cabe en el tablero, no sale de la máquina.*
- **[ADR-0009](docs/adr/ADR-0009-el-daemon-observa-no-ejecuta.md)** / **[ADR-0010](docs/adr/ADR-0010-el-plano-de-operar.md)** — las negativas siguen siendo prominentes; ADR-0010 permite crear y reanudar trabajo sin crear una vía de publicación.

## Dos cicatrices que se convirtieron en diseño

**El puerto como lock** no es estética. `ship.sh` del harness usaba `mkdir` y
escribía el pid *después*: un SIGKILL en esa ventana dejaba un lock **inmortal**
que mataba todos los ships de ese repo, para siempre, hasta un `rm -rf` a mano.
Lo reprodujimos. El kernel libera un puerto al morir el proceso, atómicamente,
siempre. No hay ventana que cerrar.

**El costo como vista** tampoco. La primera versión del panel tenía un precio
por defecto de Opus: corriendo GLM te habría cobrado tarifa de Opus y te lo
habría enseñado con dos decimales, como si fuera un dato. Un número inventado
con aspecto de hecho es peor que un hueco honesto.

## Release

`brew install harness` trae daemon + panel en **un** binario. El panel viaja
embebido (`//go:embed internal/webui/dist`), así que cortar un release es
hornear ese embed fresco y taggear. Un comando:

```bash
make release VERSION=0.58.0
```

Qué hace: sincroniza los assets embebidos desde el installer con
`scripts/sync-assets.sh` (el **mismo path** que el gate de `release.yml`
verifica con `--check`), corre los tests, commitea, taggea `v0.58.0` y lo
pushea. El tag dispara `release.yml`: construye las 4 plataformas, publica el
release y actualiza la fórmula en el tap (`brew`).

**Si cambiaste el panel**, actualiza primero el dist del installer desde
harness-ui (la cadena es `harness-ui → harness-creator → harness-daemon`):

```bash
# en el repo del installer (harness-creator):
bash scripts/sync-ui.sh          # rebuild del dist desde harness-ui
git commit -am "sync panel" && git push
# luego, aquí:
make release VERSION=0.58.0
```

`make ui` reconstruye el embed directo desde harness-ui — es **preview local**;
el release siempre pasa por el installer (por el gate). `make release` cubre el
último salto; `sync-ui.sh` cubre el primero cuando la UI cambió.

### Actualizar el panel (usuario)

El daemon corre en background y sirve el panel. Tras un `brew upgrade harness`,
el proceso viejo sigue vivo con la versión anterior — por eso a veces ves la UI
vieja. Un comando lo reinicia con el binario nuevo:

```bash
harness ui --reload        # para el viejo + arranca el nuevo + abre el panel
harness ui -d              # solo lo levanta en background, sin abrir navegador
harness ui --port 8080     # elige puerto (default: config.json > 7180)
```

### Agentes de tarea (Nueva tarea → selector)

El form «Nueva tarea» ofrece un **agente** (CLI) y un **modo**. Built-in:
`claude` (usa `/auto`, corre headless o en tab de herdr), `opencode` y `kimi`
(prompt crudo, solo tab de herdr). Se personaliza en `config.json` sin tocar el
binario (`GET /api/task-agents` los expone):

```json
{
  "task_agents": [
    { "name": "claude",   "bin": "claude",   "auto": true, "headless": true },
    { "name": "opencode", "bin": "opencode" },
    { "name": "kimi",     "bin": "kimi" }
  ]
}
```

`auto`: pasa `/auto <id>` (pipeline del harness, solo Claude Code). `headless`:
soporta `-p` en segundo plano; si no, solo corre en un tab de herdr (visible).

## Relación con `harness-creator`

Repos separados, a propósito: otro lenguaje, otro ciclo de vida, otra audiencia
— y este funciona **sin** harness. El plugin lo consume (pinnea su versión en
`daemon.lock`), no lo contiene.

---

**Licencia**: MIT · **Autor**: Andres Garcia
