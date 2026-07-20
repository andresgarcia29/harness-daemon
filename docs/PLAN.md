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

## Fase 3 — Ingesta ✅ (2026-07-17)

Implementada en `internal/store` (SQLite `modernc.org/sqlite` puro Go — la
matriz darwin/linux × arm64/amd64 sigue compilando con `CGO_ENABLED=0`;
migraciones embebidas forward-only con backup `.bak-<NNN>` antes de aplicar) e
`internal/collect` (tailer con offsets persistidos + adaptador Claude Code +
bus). Verificado contra transcripts REALES: 1,299 llamadas / 50 agentes / 3
modelos ingeridos de 2 sesiones, y un evento del bus emitido en vivo apareció
en `/api/stats` al siguiente tick.

- Polling a 2s en el loop de `run` (la nota de kqueue de abajo sigue vigente);
  `/api/stats` expone los conteos — la prueba de vida es que crecen.
- Las leyes portadas del panel, ahora con test en Go: dedupe por `message_id`
  (MAX por campo, no suma), `<synthetic>` fuera, reloj del record jamás el
  nuestro, `ok` string→int normalizado en la ingesta, desglose de caché 5m/1h
  en columnas SEPARADAS (el panel no distinguía; el esquema sí).
- `go test ./...`: migración idempotente, dedupe, costo-es-vista (sin precio →
  NULL; agregar precio re-cotiza el histórico solo), uid de eventos estable,
  tick completo idempotente (re-ingesta total = mismas filas), tail incremental
  con línea a medio escribir.
- El esquema canónico vive ahora en `internal/store/migrations/` (go:embed no
  alcanza fuera del paquete; dos copias del único archivo caro era divergencia
  con fecha). `db/migrations/README.md` apunta ahí.
- Precios builtin sembrados al arranque (`SeedBuiltinPrices`, ON CONFLICT DO
  NOTHING: la edición del usuario gana para siempre) y `/api/stats` cotiza:
  `costs.cost_usd` viaja SIEMPRE con `unpriced_calls` — un total que calla lo
  que no pudo cotizar es un número inventado con aspecto de dato. Contra los
  transcripts reales: $396 estimados, 1309/1309 cotizables. (El panel Python
  estima $434 sobre las mismas fuentes: métodos de corte distintos — ambos son
  ESTIMADOS y lo dicen; la báscula oficial sigue siendo ccusage.)
- **Pendiente de la fase**: sync de precios LiteLLM/OpenRouter (el panel ya lo
  hace contra OpenRouter — portarlo); rotación `(dev,ino)` fina (hoy:
  `size < offset` → relee, y los UPSERTs hacen la relectura gratis).

## Fase 4 — Que el harness cuente lo que decide

Sin esto el tablero solo sabe de agentes, y **el harness no son sus agentes: son
sus decisiones y sus negativas.**

- `ship.sh` emite cada gate al bus (nombre, veredicto, remediación si bloqueó)
- `/auto` emite fase, supuestos del ledger, y paradas
- `deploy-watch.sh` emite el canary

## Fase 1 — El daemon es el backend ✅ (2026-07-17)

`internal/webui` embebe el build de React (go:embed; Node solo para construir),
`internal/api` arma el snapshot desde SQLite con el MISMO contrato que el panel
de Python. `run` sirve `/`, `/api/state` y `/api/stream` (SSE cada 2 s).

Verificado sirviendo el panel DESDE el daemon contra corvux: mission-control,
6 sesiones reales, gantt con pico 3 (tiempos de agente derivados de las
llamadas, no del mtime), Gastos con barras/dona/tabla, $154 cotizado. El chip
dice DAEMON y "Operar" se oculta (op:false: solo lectura por ahora). El
frontend cae con gracia en lo que el daemon aún no sirve (op, hilo de
razonamiento, chips de git, Docs/Skills) — se suman en fases siguientes.

**Paridad de lectura (2026-07-17, cont.):** `internal/api/inventory.go` porta
Docs (comandos/agentes/gates/hooks/make), Skills & MCP (inventario + checks
estáticos; la sonda viva es OPERAR, no se corre), `/api/task-git` (git+gh) y
`/api/task-events` (arco completo del grafo). Verificado desde el daemon contra
corvux: Docs con 8 comandos/14 agentes, 5 MCPs, el grafo de 63 eventos, el chip
"directo a main" del widget.

**Razonamiento (2026-07-17):** migración `002_thread` + el colector persiste
el hilo por agente (texto/pensamiento/tool, REDACTADO antes del disco vía
`internal/redact`; seq = offset×100+bloque → idempotente). `/api/session` lo
sirve. Verificado desde el daemon contra corvux: 16 agentes, 708 items,
pausas/duraciones/costo, sin secretos crudos. **Paridad de LECTURA completa** —
el daemon sirve todo lo que el panel de Python observa.

Un bug real que salió aquí: `SetMaxOpenConns(1)` colgaba las lecturas del API
detrás de las escrituras del colector. WAL da lectores concurrentes, pero no
con una sola conexión → subido a 4 (1 escritor + N lectores).

## herdr — la capa de ejecución, LEÍDA ✅ (2026-07-17)

`internal/herdr` lee `herdr api snapshot` (workspaces/tabs/panes + agent_status:
idle/working/blocked/done/unknown) y `herdr pane read <id> --source visible`
(terminal en vivo, redactado). Endpoints `/api/herdr` y `/api/herdr/pane`. La
vista "Terminales" del panel muestra TODAS las terminales de agentes de la
máquina, cross-workspace, con su estado y su terminal en vivo (relee cada 2.5s).

**Portable y opcional**: se detecta en runtime (`LookPath` + prueba el server).
Si herdr no está o no corre → `available:false` con la razón y la vista enseña
qué es y cómo instalarlo; el resto del panel funciona igual. Nada hardcodeado.

Verificado contra herdr 0.7.3 real: workspaces corvux/latam/epicgames leídos,
y el terminal en vivo de un pane demo renderizado en el panel (paso 1/6…4/6).

## El plano de OPERAR en Go + control por PTY ✅ (2026-07-17)

`internal/api/op.go`: el plano completo, portado del panel de Python con los
mismos guardrails (token por arranque inyectado en el HTML + X-Corvux-Token +
check de Host + límite de body). Endpoints: op/task (task.md + claude -p /auto
con --session-id conocido), op/respond (--resume), op/connect (valida contra el
proveedor ANTES de guardar, 0600), op/sync-prices, y el nuevo **op/pane-send**:
responder a CUALQUIER agente por su PTY de herdr (pane_id validado contra el
snapshot vivo; texto acotado; jamás strings arbitrarios al CLI). El snapshot
reporta op:true y el frontend enciende Operar + el responder de Terminales.

Precios: **sync automático desde OpenRouter al arranque** (solo modelos
observados sin precio, fail-open) + botón. Persisten en la tabla prices
(source=openrouter) — cada máquina sincroniza igual → mismos precios en todas.
`/api/db` expone el estado real del almacén (motor/ruta/tamaño/filas) para la
tarjeta de Conexiones; Postgres queda anunciado sin botón hasta el modo nube.

herdr viaja DENTRO del snapshot SSE (tiempo real, sin polling extra); el texto
de un pane abierto se relee cada 1.5 s.

Verificado end-to-end con herdr real: un pane de eco recibió texto enviado
DESDE LA UI ("desde la UI: todo verificado") y su respuesta se renderizó en el
panel; sin token → 403. Tests Go: guardas (token/Host), task con stub de
claude, respond --resume, pane-send valida, connect con httptest (401 no
guarda; 200 guarda 0600), sync de precios con catálogo falso.

## herdr como plano de control completo ✅ (2026-07-17)

Ciclo de vida (op/herdr): interrumpir (Ctrl-C), cerrar pane/tab/workspace,
detener la sesión entera — todo valida el id contra el snapshot y confirma en
la UI. Respuestas interactivas (op/herdr-key): el panel PARSEA el menú del
agente del terminal y ofrece botones; las teclas se filtran a un set seguro.
Abrir (op/herdr-open): workspace, terminal (tab), split de pane. Detección del
PROGRAMA por pane (process-info → Claude Code/Kimi/Codex/Vertex…), cache 15 s.

El PUENTE herdr↔harness: el snapshot expone workspace{name,path}; el frontend
enlaza cada pane cuyo cwd está en el workspace (o en worktrees/<tarea>/) con su
pipeline del bus, y muestra una tira "◈ <ws> · <tarea> · <fase> · <estado>" en
la ventana del terminal, clic → la tarea. Verificado: un pane en
worktrees/COR-DEMO/widget mostró IMPLEMENT · TRABAJANDO con su mini-pipeline.

Pendiente: adaptadores de archivo (OpenCode serve / Codex / Kimi) para máquinas
sin herdr, y el modo nube (Fase 7).

## Fase 5 — El tablero

React + Vite + TS, **embebido en el binario** con `embed.FS` (Node solo en CI,
nunca en la máquina del usuario).

> Preact se descartó: su argumento es el tamaño del bundle, y **ese argumento se
> muere en localhost**. 45KB desde 127.0.0.1 son 0ms. Gana lo que el equipo ya
> mantiene.

Vistas: **el estado grande** (TRABAJANDO / TE ESPERA / BLOQUEADO / LISTO) ·
pipeline · agentes en paralelo · **Historia** (la narrativa de decisiones) ·
tokens.

> **Estado 2026-07-17**: el panel Python del plugin (v0.19.0) ya implementa
> estas vistas Y el plano de operar ([ADR-0010](adr/ADR-0010-el-plano-de-operar.md)):
> crear tareas que lanzan `claude -p "/auto …"` headless, responder a agentes
> por `--resume`, conexiones validadas (Linear/OpenRouter) y sync de precios.
> Esta fase migra ese diseño ya probado a React embebido — no lo re-inventa.
> El contrato de datos (snapshot JSON + bus JSONL) y las defensas (token
> anti-CSRF por arranque, Host check, 127.0.0.1) vienen probados por
> `tests/` del instalador.

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

- **Opera sin publicar.** Puede crear y reanudar trabajo (ADR-0010), pero no
  aprobar, mergear ni saltarse gates. ADR-0010 reemplaza parcialmente ADR-0009.
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

---

## Nota de método: no construyas UI a ciegas

La UI del panel se construyó dos veces sin poder verla, y las dos salió mal —
la segunda vez hicieron falta cuatro capturas del usuario para descubrir que era
un muro ilegible. Los tests de contrato pasaban en verde todo el tiempo: medían
que no faltara un campo y que el SSE empujara. Medían lo que se podía medir, no
lo que importaba.

El arreglo es barato y hay que usarlo SIEMPRE que se toque el front:

```bash
npm i puppeteer-core          # usa el Chrome que ya tienes; no descarga nada
node shot.js                  # captura las 4 pestañas y reporta errores JS
```

Chrome headless directo es inestable en macOS (GCM, allocator); puppeteer-core
apuntando a `/Applications/Google Chrome.app/...` funciona a la primera.

**Regla: ningún cambio de front se commitea sin una captura mirada.**
