# El plan

Ordenado por **lo que es caro de cambiar después**, no por lo que se ve bonito
antes. Las decisiones irreversibles primero; los píxeles al final.

## Principio de secuencia

> Binarios, frameworks y vistas se tiran y se rehacen en una tarde.
> **El esquema y los ids son para siempre.**

Todo lo que la nube necesita (el `Sink` como interfaz, `machine_id`,
workspace-por-remote, el buffer por offsets) se decide en el build **local**. Si
esas costuras quedan bien, el chart de K8s es una tarde. Si quedan mal, el chart
es reescribir. Por eso la nube va al final aunque esté considerada desde la
primera línea.

---

## Fase 0 — El bug que no podía esperar ✅

**Hecho** (`harness-creator` v0.12.0).

`track-read.sh` leía `.harness/current-task`: **un archivo global**. Con diez
sesiones abiertas, la segunda pisaba a la primera y la evidencia se apuntaba en
la tarea equivocada — un gate podía pasar con evidencia de otro trabajo. Y peor:
**nadie escribía ese archivo**, así que el hook salía sin registrar nada y
`gate_evidence` bloqueaba *todos* los ships, para siempre.

Arreglo: **la tarea se DERIVA de la ruta** (`worktrees/<task>/<repo>/…`), no se
guarda. Sin estado compartido no hay estado que corromper. Probado con dos
sesiones concurrentes: cero contaminación cruzada.

## Fase 1 — Identidad y esquema ✅

**Hecho.** [`db/migrations/001_init.sql`](../db/migrations/001_init.sql) +
`internal/ident`. Lo único irreversible del proyecto:

- `machine_id` UUID (no hostname) · workspace por **git remote** (no ruta)
- `message_id` como **PRIMARY KEY** → el doble conteo es imposible, no
  "improbable si el código está bien". Y como el UPSERT es idempotente:
  **at-least-once en el cable + escrituras idempotentes = exactly-once**, sin
  transacciones distribuidas.
- **El costo es una VISTA** → agregar el precio de GLM el mes que viene
  re-cotiza todo el histórico solo.

## Fase 2 — El singleton ✅

**Hecho.** `internal/lock`. El puerto es el lock. Probado: 10 `ensure`
concurrentes → 1 daemon; `kill -9` → el kernel libera el puerto → cero basura.

---

## Fase 3 — Ingesta (siguiente)

- Driver SQLite (`modernc.org/sqlite`, puro Go — sin cgo, para cross-compilar
  sin dolor) + migraciones forward-only + backup antes de migrar
- Tailer de JSONL: offsets `(dev, ino)` para rotación, buffer del último renglón
  parcial, decoder incremental de UTF-8 (un `read()` parte secuencias multibyte)
- **Polling, no kqueue/inotify.** El tailer ya hace `fstat` para detectar
  truncado, así que el polling no cuesta ni una syscall de más: 10 archivos a
  250ms ≈ 40 fstat/s ≈ 0.004% de un core. kqueue compra 249ms que nadie percibe,
  a cambio de una rama solo-macOS **más** el fallback de polling que escribirías igual.
- Adaptador Claude Code: transcripts + `subagents/agent-*.meta.json` (el grafo
  padre→hijo sale de `toolUseId`/`spawnDepth`; no se infiere)
- Registro de precios: builtin + sync de LiteLLM + override local

## Fase 4 — Que el harness cuente lo que decide

Sin esto el tablero solo sabe de agentes, y **el harness no son sus agentes: son
sus decisiones y sus negativas.**

- `ship.sh` emite cada gate al bus (nombre, veredicto, remediación si bloqueó)
- `/auto` emite fase, supuestos del ledger, y paradas
- `deploy-watch.sh` emite el canary

## Fase 5 — El tablero

React + Vite + TS, **embebido en el binario** con `embed.FS` (Node solo en CI,
nunca en la máquina del usuario).

> Preact se descartó: su argumento es el tamaño del bundle, y **ese argumento se
> muere en localhost**. 45KB desde 127.0.0.1 son 0ms. Gana lo que el equipo ya
> mantiene.

Vistas: **el estado grande** (TRABAJANDO / TE ESPERA / BLOQUEADO / LISTO) ·
pipeline · agentes en paralelo · **Historia** (la narrativa de decisiones) ·
tokens.

## Fase 6 — Update firmado

Temp → verificar minisign → `--selftest` → `rename()` → reiniciar →
`.prev` + revert si `/health` no responde en 5s. Supervisado por bash.
Pin en el plugin (`daemon.lock`). Ver [ADR-0006](adr/ADR-0006-auto-update.md).

## Fase 7 — La nube

`Sink` remoto + modo `serve` + chart. **1 Deployment, 1 PVC, 1 Service,
1 Ingress, 1 Secret.** Si el chart crece a Postgres + Redis + una cola,
perdimos: es un binario de 10MB que escribe en un archivo.

Un deployment **por cliente**. Cero código de tenancy — es donde viven los bugs
de "vi datos de otro cliente", y el upside es ninguno.

---

## Lo que este proyecto NO hace

- **No actúa.** Sin botones de aprobar/reintentar/matar ([ADR-0009](adr/ADR-0009-el-daemon-observa-no-ejecuta.md)).
- **No hay streaming token a token.** Medido: un agente vivo dejó su transcript
  quieto 36s y luego escribió 52KB de golpe. Claude Code escribe los mensajes al
  cerrarlos. Mostramos por turno y lo decimos. Un typewriter falso sería teatro
  en la herramienta cuyo trabajo es observar con honestidad.
- **No inventa costos.** Modelo sin precio → `—`.
- **No hace multi-tenancy.**

## La advertencia que va en el proyecto, no en una nota al pie

Un tablero bonito encima de gates que **nunca hemos evaluado** es una mentira
muy convincente. El harness sigue sin evals: este panel hace que **se entienda**,
no que sea **correcto**. Ambas cosas valen; no son la misma, y sabemos cuál se
ve mejor en una demo.

Por eso las negativas se enseñan igual de grandes que los éxitos. *"El gate
bloqueó a su propio agente por debilitar un test"* es la línea más persuasiva
del producto — y la única que no se puede fingir.
