# MinionS: descomposición y fan-out (el supervisor parte, los workers responden)

> Doc de capacidad del plugin (PROPUESTA, opt-in). No es un ADR de tu
> instancia: vive en docs/harness/ para no colisionar con tu numeración de
> ADRs. Si lo ratificas, registra TU decisión como el ADR que te toque.

- **Estado**: PROPUESTO (opt-in, default OFF; requiere ratificación humana)
- **Fecha**: 2026-07-22

## Qué es (y qué NO es)

El patrón MinionS ([arXiv:2502.15964](https://arxiv.org/pdf/2502.15964),
HazyResearch) **NO es elegir un modelo más barato para ahorrar**. Es un
protocolo de ORQUESTACIÓN por descomposición:

1. un **supervisor** (el tier caro/listo, `deep`) parte una tarea en
   **sub-preguntas ACOTADAS y con scope** (la descomposición es el acto
   inteligente);
2. muchos **workers** (el tier barato, `worker`) responden **cada
   sub-pregunta EN PARALELO**, viendo SOLO su trozo de contexto;
3. el supervisor **SINTETIZA** sobre las respuestas citadas, gastando sus
   tokens caros en JUICIO, no en lectura.

El ahorro de tokens, dinero y tiempo es una **consecuencia** de descomponer
bien, no el objetivo. El objetivo: calidad de frontier con el throughput de
muchos workers dirigidos. MinionS recupera 97.9% de la calidad del modelo
caro a 5.7× menos costo — porque el bruto lo hacen los workers en paralelo,
no porque uses un modelo peor.

## Por qué el harness YA es medio-minions

Su columna vertebral ya descompone: el architect parte el RFC en un DAG de
tareas y los implementers corren en paralelo. Eso es MinionS a nivel de
IMPLEMENTACIÓN. Lo que esta capacidad añade es descomposición un nivel
ARRIBA: en el RAZONAMIENTO que produce el plan y el litigio, hoy monolítico.

## El mecanismo (opt-in: `minion_decompose: true`)

En la fase RFC, con la bandera activa:

1. **El architect (supervisor) emite `tasks/<id>/probes.json`**: sub-preguntas
   factuales con scope. Ejemplo:
   ```json
   [
     {"id":"own-booking","q":"¿Qué servicio posee la tabla bookings y quién escribe?","scope":{"graph":"bookings"}},
     {"id":"proto-compat","q":"¿El cambio al proto rate-limit es expand/contract?","scope":{"files":["repos/proto/rate.proto"]}},
     {"id":"consumers","q":"¿Qué servicios llaman a /ratelimit?","scope":{"graph":"ratelimit endpoint"}}
   ]
   ```
2. **`scripts/minion-probe.sh <task> probes.json`** hace el fan-out: un worker
   barato por probe, EN PARALELO (cap = min(8, núcleos)), cada uno viendo solo
   su scope. Escribe `tasks/<id>/probes/<qid>.md` (citado).
3. **El architect sintetiza** el plan + delta-spec sobre el pack de respuestas.

La red de seguridad: cada worker CITA `archivo:línea` y dice `DESCONOCIDO` si
su scope no responde; el supervisor ve qué quedó incierto y tira de la fuente
cruda. Nunca sintetiza a ciegas.

## Alternativas rechazadas

| Alternativa | Por qué no |
|---|---|
| Un tier `reader` que resume un blob (lo que se propuso antes) | Es selección de modelo disfrazada: el acto "inteligente" (qué extraer) queda en un solo modelo leyendo todo. NO es MinionS |
| MinionS default-ON | Viola la ley del harness (arquitectura la ratifica un humano). Dos+ rondas del supervisor donde había una es cambio real |
| Un MCP dedicado | Un script + el CLI basta (regla CLI > MCP) |

## Cómo ratificar y medir

1. `minion_decompose: true` en harness-answers.yaml de una instancia.
2. Correr `/auto` sobre una tarea FULL (cruza ownership, varios repos): ahí es
   donde el supervisor tiene más que razonar y la descomposición paga.
3. Comparar en el panel (Gastos): el input del architect debería caer y el
   plan NO debería perder ownership/invariantes (los abogados son la red).
4. Si el plan mejora o iguala a menor costo → default ON en un ADR sucesor. Si
   no → queda como capacidad opt-in documentada.
