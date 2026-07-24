---
name: harness-bug-report
description: Verifica y reporta upstream un bug del HARNESS mismo (scripts, hooks, comandos, gates, panel, cronjobs del plugin harness-creator), no del código del proyecto. Úsala SIEMPRE que un artefacto del harness falle, se comporte distinto a lo que documenta, o te obligue a un workaround: te hace verificar que el bug es real y que vale la pena arreglarlo, y levanta el issue en el repo del plugin. Dispara sola, sin que el humano la pida.
---

# harness-bug-report: el bug del harness no muere en tu máquina

REGLA AUTOMÁTICA del workspace: si tropiezas con un bug del **harness**,
verificas y reportas. No esperas a que te lo pidan, y tampoco lo rodeas con un
workaround silencioso: un workaround local condena al siguiente usuario a
tropezar con lo mismo. Pero el reporte falso es peor que el bug no reportado
(entierra los reales), así que el filtro de abajo es obligatorio y es
fail-closed: **si una verificación no pasa, no hay issue.**

Lo verificable lo hace `scripts/harness-bug.sh` (propiedad del artefacto,
drift local, versión, dedupe, cuota, redacción). Lo que pones tú es el juicio:
¿es real, y vale la pena arreglarlo?

## Paso 0: ¿es un bug DEL HARNESS?

| Lo que ves | Qué es |
|---|---|
| `scripts/*.sh` o `*.py` del harness revienta, o hace algo distinto a lo que dice su cabecera | bug del harness ✓ |
| un hook bloquea (o deja pasar) lo que su ley NO dice | bug del harness ✓ |
| un gate de ship.sh falla con entrada válida, o pasa con entrada inválida | bug del harness ✓ |
| el doctor reporta verde algo roto (o rojo algo sano) | bug del harness ✓ |
| un comando del pipeline documenta un flag/contrato que no existe | bug del harness ✓ |
| tus tests, tu build, tu servicio | bug TUYO: arréglalo en tu repo |
| tu paso custom, tu spec, tu abogado, tu answers | artefacto de tu instancia: arréglalo aquí |
| falta un CLI/MCP que elegiste | configuración: `scripts/bootstrap.sh`, no issue |
| te falta una capacidad que el harness nunca prometió | feature request, no bug |

`scripts/harness-bug.sh check <ruta>` decide la primera columna sin opinión:
propiedad del artefacto y si está personalizado localmente.

## Paso 1: verifica que es REAL (las cinco, en orden)

1. **Reprodúcelo dos veces, en shell limpia y con `bash -c`.** La sesión es
   zsh y su word-splitting da resultados falsos (ley 10 del workspace). Una
   falla que no se repite no es un bug: es un estado.
2. **Redúcelo al mínimo**: el repro NO puede depender de tus repos privados,
   tus secretos ni tu red. Si solo falla con tu workspace, todavía no sabes
   qué falla. Baja hasta el artefacto del harness solo, con entradas de
   juguete (un `mktemp -d`, dos archivos falsos).
3. **Lee el contrato antes de acusar**: la cabecera del script, su doc en
   `docs/harness/` y el CLAUDE.md. Un comportamiento documentado que no te
   gusta no es un bug, es un desacuerdo de diseño (eso va como feature
   request, con otra conversación).
4. **Descarta que sea TUYO**: ¿el archivo está parcheado localmente? ¿tu
   answers declara algo que el script asume distinto? `harness-bug.sh check`
   te dice si hay drift contra el template del plugin.
5. **Descarta que ya esté arreglado**: si tu instancia está atrasada, corre
   `/harness-update` y re-verifica. Reportar un bug ya corregido es el error
   más frecuente de este canal (`harness-bug.sh report` lo bloquea solo).

Guarda la salida del repro en un archivo (`tasks/<id>/harness-bug-<slug>.log`
o `.cache/`): es el `--repro` obligatorio.

## Paso 2: ¿VALE LA PENA arreglarlo?

Un bug real que no vale la pena arreglar tampoco se reporta. Responde las
tres; si alguna es "no", cierra el asunto con una nota al humano y sigue:

- **¿Le pasa a alguien más?** Si depende de tu layout, tu versión de una
  herramienta exótica o tu parche local, no. Eso es el `--impact` y es
  obligatorio escribirlo.
- **¿Qué cuesta?** Bloquea un ship / corrompe estado / miente en verde
  (reportar SIEMPRE) · fricción repetida (reportar) · cosmético una vez
  (no, salvo que la corrección sea de una línea y la propongas tú).
- **¿Es arreglable upstream sin romper a los demás?** Si el fix exige que el
  plugin adivine tu entorno, no es un bug del plugin: es una capacidad que
  falta, y va como feature request.

## Paso 3: reporta

```bash
scripts/harness-bug.sh report \
  --title "ship.sh --precheck interpreta el flag como task-id en bash 3.2" \
  --file scripts/ship.sh \
  --repro tasks/COR-42/harness-bug-precheck.log \
  --impact "cualquier instancia en macOS: --precheck es el paso previo a review de TODA tarea" \
  --dry-run          # míralo antes de publicar; quítalo para abrir el issue
```

El script verifica, redacta secretos, deduplica (local y remoto), respeta la
cuota de 3 issues/24h y publica en el repo del plugin. Sale distinto de cero
con la razón exacta cuando NO procede: propiedad (3), repro ausente (4), cuota
(5), instancia atrasada (6), drift local (7), canal apagado (8).

**Antes de publicar, lee el cuerpo del `--dry-run` completo.** Sale a un repo
PÚBLICO: nombres de tus repos privados, hosts internos, rutas con tu usuario y
IDs de tickets se quitan a mano (la redacción automática solo cubre patrones de
secretos). El repro debe ser genérico: si no lo es, vuelve al paso 1.2.

## Después

- Emite al bus lo que decidiste: reportado (con URL) o descartado (con la
  razón). El humano lee el panel, no tu consola.
- Si tuviste que poner un workaround local para seguir, déjalo comentado con
  la URL del issue y quítalo cuando llegue el fix.
- Si el fix es de una línea y lo tienes claro, dilo en el issue (o manda el PR):
  un issue con diagnóstico y parche se arregla el mismo día.
- Nunca desactives un gate ni un hook para esquivar un bug del harness. Eso no
  es un workaround: es apagar la red de seguridad de todo el workspace.
