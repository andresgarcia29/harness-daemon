# ADR-0007 — Si no cabe en el tablero, no sale de la máquina

`status: ACCEPTED` · 2026-07-16

## Contexto

En modo cloud los datos salen del equipo. La DB contiene rutas, texto de
tickets y trazas de trabajo sobre código de clientes.

## Decisión

**Regla única:** *si no cabe en el tablero, no sale de la máquina.*

**Sale:** ids, conteos, fases, gates (y si bloquearon), decisiones, el ledger de
supuestos, modelo, tokens, costo, timings.

**No sale nunca:** output crudo del agente, diffs, contenido de archivos,
valores de secretos (jamás, en ningún modo).

**Los prompts del usuario: no por defecto, pero posible** (decisión del
2026-07-17). Se modela como `daemon.privacy: metadata | full` en
`harness-answers.yaml` — POR WORKSPACE, porque una laptop sirve a varios
clientes: el workspace de un cliente sensible se queda en `metadata` mientras
un proyecto propio puede subir texto si le sirve. `metadata` es el default y
lo que no se elige explícitamente no viaja.

## Por qué esta regla y no otra

Porque el objetivo declarado del producto es que **se entienda hasta si no eres
técnico** — y un resumen para un no-técnico **nunca contiene código de cliente**.
La restricción de privacidad y la meta de producto resultan ser **la misma
regla**. Es raro; hay que aprovecharlo.

El texto crudo no se pierde: vive en los transcripts, en tu disco, donde ya estaba.

## Consecuencias

- El colector redacta ANTES de escribir (patrones de GitHub/Vault/JWT/AWS/
  Slack/Linear + `clave: valor`), igual que ya hacen los hooks. Defensa en
  profundidad: redactar en el productor y otra vez en el que sirve.
- La pestaña Historia es legible con solo esto: es la prueba de que la regla no
  nos cuesta el producto.
- Un cliente que exija que sus datos vivan en su cloud → su propio deployment
  (ADR-0008), no una feature.
