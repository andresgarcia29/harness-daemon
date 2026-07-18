# ADR-0011 — El plano de init: el wizard que construye el harness antes de que exista la ley

Estado: ACEPTADO · 2026-07-18

## Contexto

`harness init` abre un wizard web que lleva de cero a harness funcionando:
carpeta, GitHub, clone, requisitos, discover, generación, MCPs, primeras
sesiones. Eso choca en apariencia con ADR-0009 ("el daemon observa, no
ejecuta") y con ADR-0005 ("un proceso, un workspace, fijado al arrancar").

## Decisión

### 1. Late binding write-once del workspace

En modo `--setup` el daemon arranca **sin** workspace. El paso 1 del wizard lo
fija **una sola vez** (`adopt`): ahí nacen el colector y el plano de operar.
No hay relanzamiento: relanzar rotaría el op-token inyectado en el HTML y
mataría la página del wizard a mitad del flujo. ADR-0005 se conserva — un
proceso, UN workspace — solo que se asigna tarde. Cambiar de workspace a mitad
de un init es 409: se termina o se borra ese init primero.

### 2. Por qué init puede ejecutar sin violar ADR-0009/0010

El plano de init ejecuta cosas (git clone, instaladores, `claude -p`). Es
legítimo porque **construye el harness antes de que exista la ley**: no hay
todavía `ship.sh`, ni hooks, ni gates que puentear. En cuanto `finish` corre el
doctor y estampa `.harness-version`, el plano de init **se apaga** (sus
mutaciones devuelven 410) y las leyes de siempre vuelven a regir completas:
a main solo por `ship.sh`, el operador jamás toca la ley, secretos write-only.

Guardrails del plano mientras vive:

- Mismo Guard que operar: solo `127.0.0.1`, Host check, token por-arranque en
  header custom, body acotado.
- **El navegador nunca manda comandos.** Solo selecciones sobre datos que el
  servidor ya tiene: rutas validadas server-side (sin `..`, bajo `$HOME` salvo
  confirmación), nombres del catálogo embebido, repos del listado que el propio
  server obtuvo. La disciplina anti-inyección de `targets.go` aplica entera.
- Secretos write-only 0600, presencia-como-bool. Sin cambios.
- **"Hecho" se verifica contra artefactos, no flags**: cada paso tiene un
  Verify (repos clonados con remote correcto, inventory parseable,
  `.harness-version` estampado). El `state.json` es cache/bitácora; la verdad
  vive en disco. Por eso reanudar es el caso normal: matar el daemon a mitad
  de un clone y volver a `harness init` retoma exactamente donde iba.

### 3. El puerto canónico del panel es 7180

`harness init` y `harness ui` resuelven puerto como flag > `config.json` >
**7180**. 7718 queda como default legacy de `harness daemon run` y 7717 era el
panel Python. Un solo número que recordar (y abrir en el firewall no hace
falta: sigue siendo solo-local; lo remoto va por SSH).

### 4. Cada paso es un subcomando CLI

Todo paso del wizard existe también como subcomando que emite JSON
(`harness discover --json`, `harness generate --answers f.yaml`). El wizard es
una piel sobre la CLI, no al revés. Consecuencias: el onboarding es
scripteable sin UI (CI, golden tests), y la instalación **remota** es el mismo
código por `ssh <target> harness <paso>` (F11) — no una segunda implementación.

## Consecuencias

- `Op` nace vacío al arrancar en setup (solo Guard/token) y el real se crea al
  adoptar (`NewOpWithToken`, mismo token). Las rutas de operar dan 409 hasta
  entonces.
- El snapshot lleva `init` (PublicState) mientras el wizard vive; el SSE
  existente lo transporta — el frontend no necesita otro canal para el estado.
  Los logs finos van aparte (`/api/init/logs`, SSE con Last-Event-ID).
- Un daemon normal (`--workspace`) con un init a medias en ese workspace lo
  re-adjunta (`initflow.Attach`) — reanudar no depende de recordar el flag.
