# ADR-0003 — JSONL es el cable, SQLite es el almacén

`status: ACCEPTED` · 2026-07-16

## Contexto

¿Archivos o base de datos? Los productores son hooks de bash. Los consumidores
quieren agregaciones sobre tiempo ("costo por modelo esta semana").

## Decisión

**Las dos, en capas distintas.**

- **JSONL = el cable.** Los hooks y `ship.sh` son bash: saben hacer
  `>> events.jsonl` y nada más. Y crítico: **el bus tiene que funcionar con el
  daemon muerto.** Un hook que dependa de un daemon vivo es un hook que te tumba
  el pipeline cuando se cae el panel. Inaceptable.
- **SQLite = el almacén.** El daemon tailea, normaliza y hace UPSERT. Guarda
  offsets: si estuvo apagado tres días, al arrancar se pone al corriente sin
  perder ni duplicar.

**SQLite en los DOS modos** (local y `serve`). En `serve` hay un solo escritor —
los colectores hablan HTTP, no tocan la DB. Volumen: ~5k filas/máquina/día. Con
diez máquinas, 50k/día. SQLite se ríe de eso.

## Consecuencias

- Un solo almacén: lo que pruebas en tu Mac es literalmente lo que corre en el
  clúster. Cero dialectos, cero "solo falla en prod".
- En K8s obliga a `replicas: 1` + `strategy: Recreate` + PVC RWO:
  **sin HA y ~10s de downtime por deploy.** Para un tablero de telemetría de
  desarrollo, aceptable — y los colectores buffean, así que no se pierde nada.
- Backups: Litestream replica a S3/GCS y restaura al arrancar el pod.

## Alternativas rechazadas

- **Postgres**: quita el baile del PVC, pero son dos almacenes que mantener y
  divergencia local↔prod. Si algún día hace falta, está detrás de la interfaz.
- **Solo JSONL**: sin restricción de unicidad (→ doble conteo) y hay que
  reescanear todo en cada consulta.
- **Solo SQLite (sin cable)**: obligaría a los hooks de bash a hablar SQLite, o a
  depender de un daemon vivo. Las dos cosas son peores.
