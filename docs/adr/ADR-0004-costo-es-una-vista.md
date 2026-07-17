# ADR-0004 — Guarda lo que mediste, calcula lo que inferiste

`status: ACCEPTED` · 2026-07-16

## Contexto

Queremos costo por agente/tarea/modelo, con modelos de varios proveedores
(Claude, GLM, Kimi, lo que venga) y precios que cambian.

## Decisión

**Los tokens son un hecho → columna. El dinero es una interpretación → VISTA.**

`call_costs` es un `LEFT JOIN calls × prices`. Nunca se guarda un costo.

**Un modelo sin precio cuesta NULL = desconocido, y la UI enseña "—".**
Jamás una tarifa por defecto.

> Precedente real: la primera versión del panel tenía un `_default` con precio
> de Opus. Corriendo GLM, te habría cobrado precio de Opus y te lo habría
> enseñado con dos decimales, como si fuera un hecho. **Un número inventado con
> aspecto de dato es peor que un hueco honesto.**

El precio se indexa por `(model, speed, valid_from)`: `speed: fast` es un SKU
distinto, y la caché de 5m (1.25×) y la de 1h (2×) se facturan diferente — por
eso son columnas separadas y no se suman.

## Consecuencias

- Agregar el precio de GLM el mes que viene **re-cotiza todo el histórico solo**.
- Un cambio de precios de Anthropic no pudre los datos viejos (`valid_from`).
- El modelo se guarda **tal cual se observó**; normalizar es un JOIN, no una mutación.

## Alternativas rechazadas

- **Guardar `cost_usd` en la fila**: congela una interpretación con los precios
  del día. Cambia el precio o descubres un modelo → histórico basura.
- **Tarifa por defecto para modelos desconocidos**: mentir con dos decimales.
