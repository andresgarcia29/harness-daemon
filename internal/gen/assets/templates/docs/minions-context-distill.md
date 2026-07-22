# MinionS: destilación de contexto (el reader lee, el deep razona)

> Doc de capacidad del plugin (PROPUESTA, opt-in). No es un ADR de tu
> instancia: vive en docs/harness/ para no colisionar con tu numeración de
> ADRs. Si lo ratificas, registra TU decisión como el ADR que te toque.

- **Estado**: PROPUESTO (opt-in, default OFF; requiere ratificación humana)
- **Fecha**: 2026-07-22
- **Task**: investigación pedida por el humano (Minions / orquestador)
- **Decisores**: _pendiente de ratificar_

## Contexto

El patrón MinionS ([arXiv:2502.15964](https://arxiv.org/pdf/2502.15964),
HazyResearch) parte de una observación medida: un modelo BARATO que lee un
contexto largo y lo destila, alimentando a un modelo CARO que razona sobre el
destilado, recupera 97.9% de la calidad a 5.7× menos costo (el protocolo
básico, 30.4× menos, recupera 87%).

En este harness, el tier caro (`deep` = {{MODEL_ARCHITECT}}) gasta buena parte
de sus tokens LEYENDO, no razonando: el architect lee archivos afectados + el
grafo + specs para escribir el RFC; los abogados leen el map de ownership + el
diff para litigar. Es exactamente el gasto que MinionS abarata.

El harness ya tiene la mitad del patrón en forma DETERMINISTA: `repo-brief.sh`
(estructura), `graphify` (relaciones de código), `quiet.sh` (truncado). Falta
la destilación SEMÁNTICA barata: lo que un modelo `fast` puede resumir y citar
antes de que el `deep` razone.

## Decisión

Añadir un tier **`reader`** a `models.yaml` (default `fast`) y un script
**`context-distill.sh`** que lo usa para producir un "context pack" citado en
`tasks/<id>/context/<slug>.md`. Los agentes deep (architect, abogados,
orquestador) razonan sobre el pack, no sobre el volcado crudo.

**Es OPT-IN**: nada cambia salvo que `context_distill: true` en
`harness-answers.yaml`. Con la bandera activa, el paso RFC y el litigio de
abogados anteponen una llamada a `context-distill.sh` (que corre en el
prefetch, en background, $0 en el camino caliente).

La red que hace segura la compresión: **el pack SIEMPRE cita `archivo:línea`**
y lista sus fuentes crudas; el modelo caro puede tirar de la fuente original si
el destilado no le basta. Nunca razona a ciegas. Y una sección `INCERTIDUMBRE:`
declara lo que el material no deja claro.

## Alternativas rechazadas

| Alternativa | Por qué no |
|---|---|
| Solo destilación determinista (repo-brief + grafo) | Ya existe; no captura el "qué dice este archivo sobre X" semántico que el reader sí resume |
| MinionS completo (deep descompone → N readers en paralelo → deep sintetiza) | Más calidad, más complejidad; se puede añadir después como `--deep` de context-distill si el básico prueba su valor |
| Meterlo default-ON | Viola la ley del harness (arquitectura la ratifica un humano); dos llamadas LLM donde había una es un cambio real, se gradúa con evidencia |
| Un MCP dedicado | Un MCP se gana su lugar; esto es un script y el CLI basta (regla CLI > MCP del catálogo) |

## Consecuencias

**Abarata**: los tokens caros de Fable/deep van a JUICIO, no a lectura. Medible
en ccusage: el input del architect debería caer. La calidad de razonamiento a
menudo MEJORA sobre contexto curado (menos aguja-en-pajar).

**Encarece**: una llamada `fast` extra por destilación (barata) y latencia si
no se prefetchea. Mitigado: el pack corre en background durante el prefetch, y
el stamp por hash lo vuelve $0 mientras las entradas no cambien.

**Riesgo**: el reader omite algo que el deep necesitaba. Mitigado por diseño:
cita file:línea + lista fuentes crudas + sección INCERTIDUMBRE ⇒ el deep nunca
queda ciego.

**Señal de reversión**: si tras N tareas ccusage no muestra ahorro neto (el
costo del reader ≥ lo que ahorra el deep), o los reviewers marcan más "el plan
ignoró X que sí estaba en la fuente", se apaga la bandera. Es reversible por
diseño (opt-in).

## Cómo ratificar y probar

1. Poner `context_distill: true` en `harness-answers.yaml` de una instancia.
2. Correr `/auto` en una tarea con RFC real; comparar el input de tokens del
   architect en el panel (Gastos) contra una corrida con la bandera OFF.
3. Verificar que el plan resultante no perdió ownership/invariantes citados en
   las fuentes (los abogados son la red).
4. Si el ahorro es neto y la calidad se sostiene: cambiar el default a ON en
   un ADR sucesor. Si no: queda como capacidad opt-in documentada.
