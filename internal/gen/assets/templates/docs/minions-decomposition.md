# MinionS: descomposición y fan-out (el supervisor parte, los workers responden)

> Doc de capacidad del plugin. No es un ADR de tu instancia: vive en
> docs/harness/ para no colisionar con tu numeración de ADRs. Registra TU
> decisión (dejarlo en auto, forzarlo o apagarlo) como el ADR que te toque.

- **Estado**: ACTIVO por carril (`minion_decompose: auto` = ON en
  standard/full, OFF en express). `true` fuerza siempre, `false` apaga.
- **Fecha**: 2026-07-22 (propuesta) · 2026-07-23 (default por carril)

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

## Por qué el architect es un hilo fino

Un planeador que lee 20 archivos llega a la síntesis con la ventana llena y
la atención repartida: el context rot degrada justo la parte donde queríamos
su juicio. Descomponer invierte el reparto. El architect ve briefs, grafo y
respuestas citadas, y gasta su ventana en decidir. La regla operativa está en
`.claude/agents/architect.md`: **si te falta un hecho, emites otra probe; no
abres el archivo**. Excepción única: una probe que volvió `DESCONOCIDO`
citando un archivo concreto, y solo ese rango.

## El mecanismo (`minion_decompose: auto`)

En la fase RFC, con la bandera activa (standard/full por default):

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
3. **El architect sintetiza** el plan + delta-spec sobre el pack de respuestas,
   en modo ultrathink (el plan es el artefacto que N implementers ejecutan
   sin poder preguntar).

La red de seguridad: cada worker CITA `archivo:línea` y dice `DESCONOCIDO` si
su scope no responde; el supervisor ve qué quedó incierto y tira de la fuente
cruda. Nunca sintetiza a ciegas.

Presupuesto: **2 rondas de probes**. La primera descompone lo que sabes que no
sabes; la segunda cubre lo que la primera reveló. Si tras esas dos sigue
faltando un hecho que cambiaría el plan, no es un hueco de contexto: es una
decisión que le toca a un humano, y se para.

## Alternativas rechazadas

| Alternativa | Por qué no |
|---|---|
| Un tier `reader` que resume un blob (lo que se propuso antes) | Es selección de modelo disfrazada: el acto "inteligente" (qué extraer) queda en un solo modelo leyendo todo. NO es MinionS |
| MinionS default-ON en TODOS los carriles | En express el orquestador ya tiene el contexto en su sesión: descomponer ahí compra una vuelta de reloj sin comprar juicio. Por eso el default es `auto`, no `true` |
| Un MCP dedicado | Un script + el CLI basta (regla CLI > MCP) |

## Cómo medirlo (y cómo apagarlo)

1. Corre `/auto` sobre una tarea FULL (cruza ownership, varios repos): ahí es
   donde el supervisor tiene más que razonar y la descomposición paga.
2. Compara en el panel (Gastos): el input del architect debe CAER, y el plan
   no debe perder ownership ni invariantes (los abogados son la red).
3. La otra métrica, la que importa de verdad: **rondas de review por tarea**
   (`review_rounds` en `tasks/<id>/state.json`). Un plan hecho sobre hechos
   citados se ve como tareas que pasan review en una sola ronda.
4. Si en tu proyecto no paga, `minion_decompose: false` y el architect vuelve
   a leer y razonar monolítico. La bandera es tuya; regístralo en un ADR.
