# ADR-0002 — Modelo de identidad

`status: ACCEPTED` · 2026-07-16

## Contexto

Diez sesiones de Claude Code abiertas en el mismo repo, en varias máquinas, con
subagentes que lanzan subagentes. ¿De quién es cada token, cada decisión, cada
gate? Sin una jerarquía de ids no hay atribución, y sin atribución no hay panel.

## Decisión

Seis niveles. **Ninguno se inventa: todos existen ya.**

```
máquina        machine_id   UUID en ~/.config/harness/machine-id
└── workspace  workspace_id hash del git remote NORMALIZADO
    └── sesión session_id   lo da el CLI (una terminal = una sesión)
        └── turno   prompt_id  un prompt tuyo (el trace_id que el CLI ya emite)
            └── agente  agent_id   'main' o el del subagente
                └── llamada message_id  una request a la API
```

**`machine_id` es un UUID, no el hostname.** Los hostnames colisionan
("macbook-pro") y cambian.

**`workspace_id` sale del git remote, no de la ruta.** El mismo repo es
`/Users/andres/…` en el Mac y `/home/andres/…` en el VPC. Con la ruta como
clave serían dos workspaces y centralizar no centralizaría nada. Sin remote:
`local:<machine_id>:<ruta>` — honesto, ese workspace no se puede unificar.
La ruta local es un **hecho por máquina** (tabla `workspace_paths`), no la identidad.

**`agent_id` es único por sesión**, no globalmente → PK compuesta `(session_id, id)`.

**La identidad legible de un agente es su `description`, no su `type`.** Treinta
tarjetas que dicen "general-purpose" no identifican a nadie; "Research agent
evals and sandboxing" sí. El tipo es metadata, la descripción es el nombre.

## Consecuencias

- El costo agrega hacia arriba por el árbol sin heurísticas.
- El grafo padre→hijo sale de `meta.json` (`toolUseId`, `spawnDepth`), no se infiere.
- Las tres primeras claves permiten la nube sin rediseño (ADR-0001).

## Alternativas rechazadas

- **Hostname como machine_id**: colisiona y muta.
- **Ruta como workspace_id**: rompe la centralización, que es el objetivo.
- **Un id global de agente**: no existe; el CLI solo lo garantiza por sesión.
