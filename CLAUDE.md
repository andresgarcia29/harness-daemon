# harness-daemon

Daemon Go que observa un workspace del harness y **sirve el panel** en
`127.0.0.1`. Público, se instala por brew (`brew install harness`); trae daemon
+ panel + wizard en un binario. Este archivo es un MAPA, no un manual.

## Arquitectura (3 repos, ADR-0003 del workspace corvux)

Dirección de dependencia `harness-creator → harness-daemon → harness-ui`:

- **harness-daemon** (este): observador por-máquina, `127.0.0.1-only`, **dueño
  del contrato de API** (`GET /api/version` + header `X-Harness-Api-Version`).
- **harness-ui** (privado, Vite/React): fuente de verdad del panel. Su `dist/`
  se embebe aquí vía `//go:embed internal/webui/dist`.
- **harness-creator** (privado, el installer): genera la policy del harness. El
  daemon **lee** su salida en disco (`tasks/`, `.beads/`, `.harness/runs.jsonl`,
  transcripts) — no contiene las reglas, las consume como datos. También embebe
  sus templates (`internal/gen/assets`) para el wizard `harness init`.

## Assets embebidos: la ley del sync

`internal/webui/dist` (panel) y `internal/gen/assets` (templates del installer)
son **generados**, no editados a mano. `scripts/sync-assets.sh` los reconstruye
desde el installer; el CI y `release.yml` lo verifican con `--check` (drift =
rojo). Nunca edites esos directorios directamente: corre el sync.

- `scripts/sync-assets.sh <installer>` — reconstruye (installer → daemon).
- `make ui` — reconstruye SOLO el panel directo desde harness-ui: **preview
  local**. No es el path del release (el gate verifica contra el installer).

## Contrato de tipos daemon→UI

Los structs Go de `internal/api` son la fuente de verdad; `contract/gen.go`
(`go:generate` + tygo, `optional_type:null`) emite `contract/harness.gen.ts`,
que harness-ui consume por codegen. Los json tags no cambian → wire estable.
Al tocar un payload de lectura, regenera: `go generate ./...`.

## Release

```bash
make release VERSION=x.y.z
```

Sincroniza assets desde el installer (mismo path que el gate) → tests → commit →
tag `v*` → push. El tag dispara `release.yml` (4 plataformas + tap de brew). Si
cambiaste el panel, primero `bash scripts/sync-ui.sh` en el installer y pushea
(cadena `harness-ui → harness-creator → harness-daemon`). Detalle en el README.

## Comandos

`make help` los lista. Los clave: `build`, `test`, `run`, `ui` (preview),
`release`, `dist`. Tests: `make test` (docs + vet + `go test`). Requiere el
installer clonado (default `$(HOME)/Workspace/harness-installer`) para sync/tests.

## Qué NO hacer

- No editar `internal/webui/dist` ni `internal/gen/assets` a mano (usa el sync).
- No taggear a mano para un release: usa `make release` (garantiza el sync + gate).
- No mover el contrato (`contract/harness.gen.ts`) a mano: regenéralo con tygo.
- No romper el modelo `127.0.0.1-only` ni añadir auth de red sin un ADR (el
  transporte multi-máquina es SSH/herdr; las llaves SSH son la auth).
