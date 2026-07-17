# ADR-0010 — El plano de OPERAR (revisión parcial del ADR-0009)

`status: ACCEPTED` · 2026-07-17 · decisión explícita del usuario:
*"Revierte lo que necesites, no importa. […] necesita servir todo."*

## Contexto

El ADR-0009 fijó "el panel observa, no ejecuta" para que la UI no fuera una
segunda puerta a `main`. El usuario diseñó Corvux Manager con plano de control
(crear tareas, responder al agente, conectar proveedores) y decidió que debe
funcionar.

## La distinción que lo hace seguro

**Crear trabajo ≠ mergear trabajo.** El miedo del ADR-0009 era una segunda
puerta a `main`. Pero lanzar un `/auto` desde el panel es EXACTAMENTE
equivalente a teclearlo en una terminal: lo lanzado enfrenta los mismos gates,
los mismos presupuestos y las mismas paradas. El plano de operar crea TRABAJO;
a `main` se sigue llegando por una sola puerta: `ship.sh`.

## Decisión

El panel gana un plano de operar, **separado y explícito** (sección "Operar"
en el sidebar, endpoints `/api/op/*`):

1. **Nueva tarea** — escribe `tasks/<id>/task.md` con las preferencias del
   formulario y lanza `claude -p "/auto <id>"` headless con `--session-id`
   conocido → la sesión aparece sola en el panel.
2. **Responder / pasar contexto** — reanuda una sesión por id
   (`claude -p --resume <session> "<texto>"`). La respuesta queda en el bus.
3. **Conexiones** — guarda tokens de proveedores (Linear, OpenRouter) y
   sincroniza precios de modelos.
4. **Modo nube** — Local es el modo real de hoy; VPS/K8s llegan con el modo
   `serve` del daemon (Fase 7), no como botones muertos.

## Guardrails que NO se negocian

- **`127.0.0.1` siempre.** El plano de operar jamás se expone a la red sin la
  autenticación del modo `serve` (futuro daemon).
- **Anti-CSRF real**: token por arranque inyectado en el HTML + header custom
  obligatorio + verificación de `Host`. Una página web ajena no puede postear
  a `127.0.0.1` sin pasar el preflight que nunca contestamos.
- **Secretos write-only**: el token del usuario viaja SOLO usuario→server→disco
  (`~/.config/harness/`, chmod 600). Jamás se devuelve en un GET, jamás se
  loguea, jamás pasa por un agente. El estado expone presencia (bool), nunca
  valores.
- **El operador jamás toca la ley**: no edita `ship.sh`, hooks ni
  `settings.json` (guard-canonical sigue vigente para todos).
- **Gates intactos**: nada de lo lanzado por el panel puentea un gate, un
  presupuesto ni una parada.

## Lo que el ADR-0009 conserva

La superficie de OBSERVACIÓN sigue siendo de solo lectura, y el principio de
honestidad completo (datos reales, negativas visibles, vacíos que enseñan).
