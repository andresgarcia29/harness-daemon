# ADR-0009 — El daemon observa; no ejecuta, no decide, no actúa

`status: ACCEPTED` · 2026-07-16

## Contexto

Es tentador que el panel tenga botones: "reintentar", "aprobar", "matar agente".

## Decisión

**El daemon es de solo lectura sobre el mundo.** No lanza agentes, no aprueba,
no cancela, no escribe en el workspace, no corre código del repo.

## Por qué

1. **El plano de control del harness son los comandos y los gates.** Una UI que
   además actúa es una **segunda puerta a main**, y la ley del harness es que
   solo hay una (`ship.sh`).
2. **Superficie de ataque.** Un binario auto-actualizable que además puede
   ejecutar cosas es un objetivo mucho más goloso (ADR-0006).
3. **Honestidad.** Un panel que actúa acaba enseñando lo que hace él en vez de
   lo que hace el harness.

## Corolario incómodo, y a propósito

Un tablero bonito encima de gates que **nunca hemos evaluado** es una mentira
muy convincente. Böckeler avisa de esto con los sensores: 40 checks en verde
*se sienten* como prueba de calidad, y son prueba de forma.

Por eso: **las negativas se muestran con la misma prominencia que los éxitos.**
"El gate bloqueó a su propio agente por debilitar un test" es la evidencia más
fuerte de que el harness es real, y la línea más persuasiva del producto. Un
tablero que solo enseña verdes es marketing.
