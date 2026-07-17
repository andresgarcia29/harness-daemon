# Diseña el panel de observación de un harness de agentes de código

## Qué estás diseñando

Una web app local (corre en `127.0.0.1`, un solo usuario) que observa en tiempo
real el trabajo de agentes de IA que programan solos. El sistema ("harness")
recibe un ticket o un prompt y lo lleva hasta producción sin intervención
humana: lanza agentes en paralelo, toma decisiones, y tiene "gates"
deterministas que pueden BLOQUEAR el trabajo del propio agente (por ejemplo, si
un agente debilita un test para pasar a verde, un gate lo frena).

El usuario abre este panel porque la consola del agente es una manguera de
texto: se pierde, no sabe dónde va el trabajo, ni si algo lo está esperando.

**Audiencia doble**: gente técnica Y no técnica (un PM, un cliente). Todo texto
de la UI en español llano — si una frase no la entiende alguien que no sabe qué
es un "worktree", reescríbela.

## El norte del diseño: cuatro preguntas, en este orden

1. **¿Me está esperando algo?** ← LA MÁS IMPORTANTE. El harness a veces se
   detiene porque su ley exige una decisión humana. Esa alerta debe verse desde
   el otro lado del cuarto: estado grande, color inconfundible, qué necesita.
2. **¿Dónde vamos?** — en qué fase está cada tarea (pipeline de 7 fases:
   Intake → RFC → Implement → Review → Ship → Deploy → Archive).
3. **¿Quién está corriendo?** — qué agentes están vivos AHORA y cuáles corren
   EN PARALELO (esto es temporal: se dibuja con una línea de tiempo tipo Gantt
   con barras que se solapan + una curva de concurrencia con el pico anotado,
   o algo mejor que se te ocurra).
4. **¿Cuánto llevo gastado?** — tokens y costo estimado, por agente, por sesión
   y total.

## Conceptos del dominio

- **Sesión** = una terminal con un agente corriendo. El usuario tiene hasta ~10
  abiertas a la vez en el mismo repo. **Las sesiones son la vista principal**:
  una fila/tarjeta por sesión, clic para entrar al detalle de UNA. Cada sesión
  lleva su propia cuenta — jamás se suman como si fueran una.
- **Agente** = 'main' (el orquestador) o un subagente que main lanzó. Tienen
  descripción legible ("Research sandboxing"), tipo, modelo, tokens, costo,
  timestamps de inicio/fin, y relación padre→hijo (para un árbol de "quién
  lanzó a quién" — dato real, no inferido).
- **Tarea** = una unidad de trabajo (COR-42). Tiene fase actual, origen
  (ticket|prompt), un **ledger de supuestos** ("SUPUESTO: límite 100 req/min ·
  PORQUE: la spec GW-4 lo fija · SI ES FALSO: una constante, sin migración") y
  veredictos de review.
- **Evento del bus** = lo que el harness DECIDE. Tipos: `phase`, `gate` (con
  ok=true pasó / ok=false BLOQUEÓ), `decision`, `assumption`, `stop` (parada
  que espera al humano), `ship`, `deploy` (ok true/false). Con esto se cuenta
  una HISTORIA cronológica legible: "Entró → Asumí → Decidí → Pasó el gate →
  Bloqueé → Shippeé → Deploy rojo → Te espero".
- **Tres niveles de confianza en los datos** (hoy la UI los separa en pestañas;
  puedes proponer otra estructura, pero el usuario debe saber siempre qué
  alcance mira — "esta sesión" vs "todo el workspace"):
  1. Sesiones/agentes/tokens — datos leídos de Claude Code (frágiles).
  2. El bus + tareas — datos del propio harness (estables, la capa que importa).
  3. Costos — interpretación (tokens reales × tabla de precios editable).

## Reglas de honestidad — NO NEGOCIABLES

1. **Solo observa.** Cero botones de acción (aprobar, reintentar, matar). Es un
   panel de solo lectura por diseño de seguridad.
2. **Sin streaming letra-a-letra.** El texto de los agentes llega POR TURNO
   (el agente escribe su mensaje al terminarlo; medido: 36s de silencio y luego
   52KB de golpe). No diseñes un typewriter falso: muestra mensajes completos
   con su hora y dilo ("llega por turno").
3. **El dinero no se inventa.** El costo es un estimado y se etiqueta como tal;
   un modelo sin precio en la tabla muestra "—", nunca un número. Los tokens sí
   son hechos. Debe soportar cualquier modelo (Claude, GLM, Kimi…).
4. **Las negativas pesan igual que los éxitos.** Un gate que BLOQUEÓ y una
   parada se enseñan igual de grandes que un "shipped" — son la prueba de que
   el sistema es real. Nunca un diseño que solo luzca verdes.
5. **Los vacíos enseñan, no fingen.** Sin datos de ejemplo/placeholder jamás.
   Un estado vacío explica qué lo llena y cómo.
6. **Alcance siempre declarado.** Cada número dice si es de una sesión o del
   workspace. Jamás mezclar alcances en una misma vista sin etiquetarlos.
7. Una sesión "En pausa" con actividad hace 7 min mientras el agente piensa es
   NORMAL (granularidad por turno) — el diseño puede explicarlo, no ocultarlo.

## Contrato de datos (ya existe, diseña contra esto)

`GET /api/state` devuelve el snapshot; `GET /api/stream` (SSE) empuja eventos
`snapshot` (mismo shape) y `text` ({session, agent, who, text, ts}) en vivo.

```json
{
  "workspace": "/Users/x/corvux", "warning": null,
  "sessions": [{
    "id": "e4b8baf0-…", "short": "e4b8baf0", "active": true, "idle": 7,
    "first_ts": 1784244403, "last_ts": 1784262893,
    "n_agents": 32, "n_active": 1, "peak": 25,
    "model": "claude-fable-5", "last_text": "último mensaje del orquestador…",
    "tokens": {"in": 95000, "out": 816500, "cache_read": 155800000, "cache_creation": 4900000},
    "cost": 219.53, "unpriced": [],
    "agents": [{
      "id": "main", "session": "e4b8baf0-…", "type": "main", "desc": "",
      "model": "claude-fable-5", "parent": null, "depth": 0,
      "usage": {"in": 472, "out": 394900, "cache_read": 135400000, "cache_creation": 2200000},
      "msgs": 208, "last_text": "…", "first_ts": 1784244403, "last_ts": 1784262893,
      "cost": 182.37, "priced": true, "idle": 7, "active": true, "elapsed": 18490
    }]
  }],
  "tasks": [{
    "id": "COR-42", "title": "Rate limiting por tenant", "origin": "prompt",
    "phase": "deploy", "done": ["intake","rfc","implement","review","ship"],
    "verdicts": {"pass": 1, "total": 1},
    "assumptions": ["SUPUESTO: límite 100 req/min · PORQUE: spec GW-4 · SI ES FALSO: una constante"]
  }],
  "events": [
    {"ts":"2026-07-17T05:11:42Z","kind":"gate","task":"COR-42","summary":"tests no debilitados — BLOQUEÓ el ship de atlas (exit 3)","ok":false},
    {"ts":"2026-07-17T05:11:43Z","kind":"stop","task":"COR-77","summary":"el abogado svc-hermes sigue en DRAFT — ratifica su constitución"}
  ],
  "tokens": {"in":189700,"out":1400000,"cache_read":245500000,"cache_creation":7500000},
  "cost": 369.54, "unpriced": []
}
```

Derivación de "te está esperando": el ÚLTIMO evento de una tarea es un `stop`
(morado) o un `gate` con ok=false (rojo) → pendiente.

## Stack y entrega

- **React + Vite + TypeScript**, SPA, sin backend propio (consume los dos
  endpoints de arriba). Tema oscuro de base; tipografía monoespaciada para
  números (tabular-nums). Debe verse bien de 1280px a ultrawide.
- Se va a **embeber en un binario Go** (`embed.FS`): assets estáticos, sin SSR,
  sin llamadas externas (cero CDNs — todo local).
- Entrega componentes claros y estados: cargando / vacío / con datos / con
  alerta pendiente / desconectado ("reconectando…" — SSE se recupera solo).

## Roadmap a considerar (no diseñar aún, pero no cerrar puertas)

- Modo nube: mismos datos servidos desde un servidor central; varias MÁQUINAS
  además de varias sesiones (el modelo de identidad ya lo contempla).
- Filtro por tarea, búsqueda, rango temporal.
- Vista "diagrama" del grafo de spawns (padre→hijo ya viene en los datos).

## Criterio de éxito

Alguien no técnico mira el panel 10 segundos y responde: ¿me espera algo?,
¿dónde va?, ¿quién corre?, ¿cuánto llevo? — sin que nadie le explique nada.
