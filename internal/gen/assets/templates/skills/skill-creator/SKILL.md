---
name: skill-creator
description: Detecta procedimientos repetidos del workspace y los convierte en skills bien formadas de .claude/skills/. Úsalo cuando el humano pida "crea una skill", "convierte esto en skill", o cuando notes que estás re-explicando el mismo procedimiento por tercera vez.
---

# skill-creator: el procedimiento repetido se vuelve skill

Una skill es conocimiento de PROCEDIMIENTO empaquetado: pasos que hoy
alguien re-explica (o re-descubre) cada vez. La ley del harness aplica
igual aquí: **la skill propone, los gates verifican**; una skill jamás
puentea ship.sh, hooks ni policy.

## Cuándo una skill (y cuándo NO)

| El patrón repetido es… | Entonces es… |
|---|---|
| un procedimiento multi-paso con juicio (cómo debuggear X, cómo migrar Y) | **skill** ✓ |
| un error mecánico detectable en código | regla semgrep (rule-miner), no skill |
| una decisión de arquitectura | ADR, no skill |
| un check sin juicio que un script puede hacer | gate o script de scripts/, no skill |
| algo que solo pasó una vez | nada; espera a que se repita |

Fuentes donde mirar repetición: `tasks/*/assumptions.md` (supuestos
idénticos entre tareas), `.harness/events.jsonl` (decisiones/paradas
repetidas), la memoria episódica (`mem_search`), y lo que el humano te
acaba de pedir por tercera vez.

## Anatomía de una skill bien formada

Crea `.claude/skills/<nombre-kebab>/SKILL.md`:

```markdown
---
name: <nombre-kebab>
description: <UNA frase con las palabras que dispararían su uso; es lo
  ÚNICO que el modelo ve para decidir cargarla; escríbela con los
  términos que un usuario/agente realmente diría>
---

# <nombre>: <qué resuelve en una línea>

<contexto mínimo: cuándo aplica y cuándo NO (los límites evitan que se
dispare de más)>

## Pasos
<numerados, ejecutables, con los comandos EXACTOS de este workspace
(scripts/, make), no genéricos. Cada paso verificable.>

## Verificación
<cómo saber que funcionó: comando u observable, no sensación>
```

Reglas de oro:
- **description es el 90% del valor**: sin las palabras-gatillo
  correctas, la skill existe pero nunca se carga.
- **Comandos reales del workspace**, no pseudocódigo: `scripts/ship.sh`,
  `make models`, `bd ready --json`.
- **Corta.** Una skill que necesita scroll es dos skills o es un doc.
- **Sin guion largo "—"** (ley de estilo del workspace): delata texto
  de IA. Coma, dos puntos o paréntesis.
- Si necesita archivos de apoyo (scripts, plantillas), van en el mismo
  directorio y la skill los referencia por ruta relativa.
- **Multi-herramienta**: las skills son markdown: Cursor/Kimi/otros las
  leen como playbooks vía AGENTS.md, igual que los comandos.

## Las tres capas (dónde vive cada skill)

| Capa | Dónde | Quién la actualiza | Marca |
|---|---|---|---|
| upstream | la trae el plugin | `harness update` | manifest del generador |
| compartida | TU repo de skills, declarada en `skills.yaml` | `make skills` (skills-sync) | archivo `.managed` con repo+ref+sha |
| local | `.claude/skills/<nombre>/` a secas | tú (o skill-miner) | ninguna: NADIE la pisa |

Regla de colisión: la local SIEMPRE gana; el sync reporta el choque y
no toca nada. Una `.managed` jamás se edita en sitio (edita en el repo
fuente y `make skills`).

**Promoción (el /promote de las skills)**: una local que probó su valor
en varias tareas se muda al repo compartido: `git mv` al repo de
skills, push, línea en `skills.yaml`, `make skills`. Así las demás
instancias (y máquinas) la heredan con procedencia auditable.

## Flujo de creación

1. Nombra el patrón y su evidencia (¿dónde se repitió? cita tareas/fechas).
2. Decide con la tabla de arriba que SÍ es skill (no regla/ADR/script).
3. Escribe `.claude/skills/<nombre>/SKILL.md` con la anatomía de arriba.
4. Valida: ¿la description dispararía en los casos reales que citaste?
   ¿los comandos corren tal cual en este workspace?
5. **La ratificación es humana**: si la creaste dentro de una tarea,
   anúnciala en el reporte; si la creó el cronjob skill-miner, va por PR;
   el merge es la ratificación. Una skill sin ratificar no se cita como ley.
