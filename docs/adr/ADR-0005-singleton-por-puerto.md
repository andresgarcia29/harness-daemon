# ADR-0005 — El puerto ES el lock

`status: ACCEPTED` · 2026-07-16

## Contexto

Diez sesiones en el mismo repo. Cada una podría arrancar un daemon. Queremos
exactamente uno, sin que el usuario piense en ello.

## Decisión

**Intentar `bind 127.0.0.1:7717`.** Si está tomado → `GET /health`; si es el
nuestro, salir feliz. `harnessd ensure` es idempotente: diez sesiones lo llaman
al arrancar, **uno gana el bind, nueve no hacen nada en ~5ms**.

## Por qué NO un lockfile (esto nos costó un bug real)

`ship.sh` usaba `mkdir` como lock y escribía el pid **después**. Un SIGKILL en
esa ventana dejaba un lockdir SIN pid, y el reclamo de huérfanos exigía
`[ -f pid ]`: **el lock quedaba inmortal y todo ship futuro de ese repo moría a
los 10 minutos, para siempre**, hasta que un humano hiciera `rm -rf`.
Reproducido y arreglado (harness-creator v0.11.1).

**El kernel libera el puerto al morir el proceso, atómicamente, siempre.** No
hay ventana que cerrar. `kill -9` no deja basura. Es un mutex que no se puede
corromper — y nos costó un bug aprenderlo.

## Consecuencias

- El pidfile existe solo para **informar** (`status`), nunca para excluir.
- `/health` devuelve `{name, version, protocol, pid, db}`: distingue "nuestro
  daemon" de "algo más escuchando en 7717".
- El daemon es global y `make` es por workspace: **`make stop` en un repo para
  el daemon que usaban los otros.** Se acepta a propósito: refcontar sesiones es
  reinventar los locks huérfanos, y `make init` lo revive en dos segundos.
  `make stop` avisa qué estaba observando.
