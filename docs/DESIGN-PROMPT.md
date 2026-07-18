# PROMPT MAESTRO — Sesión de diseño: rediseño de la UI del panel harness

> Pega este documento completo como primer mensaje de una sesión de Claude dedicada a diseño.
> Es autocontenido: describe el producto, el stack, la UI actual pantalla por pantalla,
> el feedback real del primer usuario y los entregables esperados. No necesitas acceso al repo
> para producir las especificaciones; todo lo relevante está aquí.

---

## 1 · Tu rol y tu misión

Eres un **diseñador de producto senior** especializado en herramientas de ingeniería
(observabilidad, paneles de operación, CLIs con UI). Tu misión: **auditar y rediseñar la UI
completa del panel local de un harness de ingeniería agéntica**, produciendo especificaciones
tan concretas que un implementador pueda aplicarlas directamente con Tailwind v4 y los
componentes shadcn/Base UI ya existentes, **sin agregar ninguna librería nueva**.

No escribas la implementación completa; escribe **specs implementables**: para cada componente
o pantalla rediseñada, el antes/después, las clases Tailwind exactas propuestas, los estados
(default/hover/focus/running/error/empty) y la justificación en una línea.

---

## 2 · Qué es el producto

Es el **panel local de un harness de ingeniería agéntica multi-repo**: un sistema donde agentes
LLM (Claude Code) trabajan sobre decenas de repositorios con gates deterministas. El panel corre
en `127.0.0.1` (solo local, nunca expuesto) y tiene **dos modos**:

**Modo 1 — Wizard de onboarding "Init" (9 pantallas).** Crea el harness desde cero:
carpeta → GitHub (token) → clonar repos → requisitos → auto-discover (script determinista que
escanea repos) → entrevista de configuración (formularios pre-llenados con evidencia) →
topología de agentes (clusters editables) → catálogo de MCPs con secretos → fin.
Puede correr **local o instalar en un VPS por SSH** (mismo flujo, la sonda corre allá).
Varios pasos son LLM (enrich, arqueología): tardan minutos y deben **narrar bitácora en vivo
con latidos** — el snapshot trae `logs_tail` (≤40 líneas) y `started` (epoch) por paso.

**Modo 2 — Panel de observación/operación.** Vistas: Resumen (dash), Tareas (con ledger de
supuestos), Sesiones de agentes, Terminales (tmux en vivo), Gastos por modelo/día, Nueva tarea,
Conexiones, Docs (ratificar documentos DRAFT), Skills & MCP (sonda JSON-RPC real, tools como
chips toggleables), y un **selector global de máquina** (local o VPS por SSH) que muta toda la
página.

**Filosofía de la casa** (es requisito de diseño, no decoración):
- *"Los agentes proponen, los sistemas deterministas verifican."* El estado nunca se declara:
  se sondea, se certifica, se demuestra.
- **Honestidad visual**: nada de typewriters falsos, spinners que mienten, ni progreso inventado.
  Un spinner solo gira si algo corre de verdad; el progreso sale de datos reales del servidor.
- **Secretos jamás en pantalla**: inputs `type=password`, se usan una vez, no se re-muestran.
- **Español** como idioma de toda la UI (tono directo, sin anglicismos innecesarios salvo
  términos técnicos: repo, cluster, MCP, tier).

---

## 3 · Stack y restricciones técnicas (DURAS — no negociables)

- **React 19 + Vite + Tailwind v4 + shadcn sobre @base-ui/react** (no Radix), `lucide-react`
  para iconos, `recharts` para charts, `sonner` para toasts.
- **Estado server-driven**: el cliente recibe un **snapshot completo por SSE cada 2 segundos**.
  El servidor es la verdad. No hay websocket por tecla ni streaming granular: **cualquier
  animación o indicador debe funcionar con actualizaciones cada 2s** (p. ej. tiempo transcurrido
  re-renderizado con cada snapshot, no un cronómetro local engañoso… aunque un tick local de
  segundos honesto sí es válido si el dato base viene del server).
- **Formularios con draft local + rev**: el usuario edita local; se guarda con número de
  revisión; si el server cambió, se ofrece "descartar y recargar". El SSE jamás pisa un input.
- **Cero dependencias nuevas de runtime.** Nada de framer-motion, headlessui, radix, CSS-in-JS,
  fuentes por CDN. Fuentes ya bundleadas: **DM Sans Variable** (headings), **Inter Variable**
  (body), Geist como fallback; monospace del sistema para todo lo técnico.
- Componentes shadcn ya disponibles: alert, alert-dialog, badge, button, card, chart, checkbox,
  dialog, dropdown-menu, field, input, label, progress, scroll-area, select, separator, sheet,
  sidebar, skeleton, sonner, table, tabs, textarea, tooltip. **Diseña con estos.**
- `tw-animate-css` está importado (animaciones utilitarias simples disponibles).
- Todo el CSS de tokens vive en `index.css` con variables OKLCH; Tailwind v4 los consume vía
  `@theme inline`. Se usan sintaxis como `text-(--ok)`, `bg-(--wait)/8`, `border-(--bad)/40`.

---

## 4 · El sistema visual actual ("Corvux Agora")

**Dos temas** (toggle en el footer del sidebar: dark → light → system):

- **Dark "True Black OLED" (default)**: `--background: oklch(0 0 0)` negro puro; cards con
  elevación sutil `#0a0a0b`; primary **indigo #6366f1**; borde hairline `#1e1e21`;
  muted-foreground `#a1a1aa`. Semánticos: `--ok:#34d399` (verde=activo/pasó),
  `--wait:#fbbf24` (ámbar=te espera), `--bad:#fb7185` (rose=bloqueó), `--brand:#818cf8`.
- **Light "Enterprise Clean"**: fondo `#f8fafc` blue-gray, cards blanco puro, texto slate
  `#0f172a`, mismo indigo primario. Semánticos con contraste sobre blanco:
  `--ok:#059669`, `--wait:#d97706`, `--bad:#e11d48`, `--brand:#4f46e5`.
- Charts: indigo/cyan/green/amber/rose. Radio base `0.625rem` con escala sm→4xl.

**Lenguaje de color semántico ya establecido** (consérvalo y sistematízalo):
verde = trabajando/pasó · ámbar = te espera una decisión · rose = un gate bloqueó ·
indigo/brand = identidad, links, agentes.

**Layout**: sidebar offcanvas ("corvux" + selector de máquina arriba; grupos "Instalación"
(solo si init activo), "Observar", "Operar", "Guía"; badges monoespaciados con conteos; footer
con dot "en vivo/reconectando…" y toggle de tema). El contenido es un **"Raised Canvas"**:
`SidebarInset` con `bg-card md:mt-2.5 md:rounded-tl-[2rem] md:border-l md:border-t` — la página
flota sobre el sidebar negro. Ancho máximo 1160px (680px en vistas de formulario).

**Primitivos propios** (en `components/bits.tsx` — son la base del sistema, rediseñarlos
propaga a todo):
- `VHead` — h1 de vista: DM Sans 2xl bold + subtítulo xs muted en la misma línea de base.
- `H2` — sección: uppercase 11.5px tracking 0.09em muted, con sub opcional.
- `Lede` — párrafo introductorio xs muted, max 86ch.
- `Story` — timeline con línea vertical y dots semánticos por beat (hard=verde lleno,
  bad/wait=aro con glow, soft=gris), hora monoespaciada, agrupación por día ("Hoy", "Ayer").
- `StatusBadge` — pill uppercase 9px; verde cuando `on`.
- `Fld` — campo de formulario: label uppercase 10.5px + hint inline + control.
- `Stats`, `PendAlert` (card ámbar/rose con gradiente), `Sup` (supuesto del ledger),
  `Empty` (card dashed con texto que dice qué hacer), `NumCell`, `Code`.

**Patrones recurrentes hoy**: cards `rounded-2xl border bg-card` con hover
`hover:border-primary/45 hover:shadow-[0_0_16px_rgba(99,102,241,.15)]`; pills de estado con
icono lucide + uppercase tracking-wider; monospace para ids/comandos/números (`tabular-nums`);
tamaños de texto muy pequeños y muy variados (9, 9.5, 10, 10.5, 11, 11.5, 12, 12.5, 13, 13.5,
15, 30px…) — **esta escala ad-hoc es uno de los problemas a sistematizar**.

---

## 5 · Inventario pantalla por pantalla (estado actual)

### Panel de observación
1. **Resumen (dash)** — 4 métricas hero clicables (Trabajando ahora / Te esperan / Costo hoy /
   En curso, con pulse cuando >0); sección "Necesita tu decisión" con TaskCards ámbar/rose;
   "Ahora mismo" con panes de terminal vivos (dot pulsante, badge del agente, "te espera/
   trabajando"); "En curso"; barras de gasto por día (divs proporcionales, sin librería);
   feed cronológico `Story` de las últimas decisiones. Estado vacío con `Empty` explicativo.
2. **Tareas** — lista de rollups por tarea (estado, fase, qué la detiene AHORA, última decisión).
3. **Detalle de tarea** — hilo completo de eventos + ledger de supuestos (`Sup`).
4. **Sesiones** — lista de sesiones de agentes (derivadas de transcripts por mtime).
5. **Detalle de sesión** — hilo del agente (agent-thread) con texto del transcript.
6. **Terminales** — panes de tmux renderizados con ANSI (pantalla `#0a0a0e` con glow radial
   sutil, cursor de bloque parpadeante, keycaps flat estilo Linear para mandar teclas; Esc en
   ámbar, kill en rose). Es la vista más "terminal-real" del panel; 561 líneas, la más compleja.
7. **Gastos** — costo por modelo y por día (recharts + tabla).
8. **Nueva tarea** — formulario angosto (680px) con `Fld`.
9. **Conexiones** — estado de conexiones (GitHub, secretos…), rediseñada recientemente.
10. **Docs** — documentos del harness; los DRAFT se ratifican desde aquí (badge "n DRAFT" en
    el sidebar).
11. **Skills & MCP** — cards por MCP con: icono de estado (sin probar/funciona/falló), nombre
    mono, badges de transporte (docker/npx/uvx/binario) y `with-secrets`, línea de comando
    truncada, fila de hechos (binario en PATH, .secrets, env, autenticado), tools reales del
    handshake como chips colapsables, error mono en rojo, hora de la sonda. Botón "Probar todos".

### Wizard Init (9 pantallas, riel lateral)
- **Stepper** (md:w-52): barra de `Progress` con % real (pasos ok/skipped del server) + lista
  de pantallas con icono de estado (check verde, X rose, spinner ámbar girando solo si running,
  dashed gris pending); clicable **solo hacia atrás** — el futuro no existe hasta que el server
  lo diga. En móvil se vuelve fila horizontal scrolleable.
- **StepShell** (marco común): título H2 + lede; banner ámbar "«paso» corriendo · 3m 12s — el
  avance va apareciendo en la bitácora" (elapsed del `started` del server, re-render por SSE);
  banner rose de fallo con el error mono y botón Reintentar; `LogTail` (bitácora ≤40 líneas,
  autoscroll con stick-to-bottom que respeta si el usuario subió a leer); fila de acciones
  abajo a la derecha.
- **Pantallas**: Bienvenida (elegir carpeta, browse-dialog) · GitHub (token) · Repositorios
  (org select → filtro de texto → lista scrolleable con checkbox + ref opcional por repo +
  "Selección actual · 3/5 clonados" con check por repo en vivo) · Requisitos · Auto-discover
  (tabs Hallazgos/Configuración: tabla de repos con rol corregible por Select, señales como
  badges, evidencia `Rec` con sparkle bajo cada campo pre-llenado, card "Enriquecimiento (LLM)"
  con Proponer/Saltar) · Agentes (clusters con chips de repos quitables + dropdown "+ repo…",
  aviso ámbar de servicios sin abogado con fix de un clic, arqueología por agente con estado) ·
  MCPs (catálogo con checkbox, evidencia sparkle, tier degradable, SecretRow con estados
  **identificado** (guardado, ámbar) → **certificado** (el MCP contestó el handshake con ese
  secreto, verde con BadgeCheck), ToolPicker de chips verdes/tachados) · Sesiones · Fin.

---

## 6 · Feedback REAL del primer usuario — REQUISITOS del rediseño

Estos dolores salieron de la primera instalación real. **Trátalos como requisitos**; varios ya
tienen una primera solución implementada (indicada), pero el rediseño debe elevarlos a patrón
de sistema:

1. **Saber siempre si algo trabaja o está atascado.** Todo proceso largo debe mostrar: tiempo
   transcurrido visible, latidos/última señal de vida, y bitácora en vivo sin tener que buscarla.
   (Parcial: banner running + LogTail. Elévalo: ¿jerarquía del banner? ¿latido visible?)
2. **Progreso por ítem que se va llenando.** Al clonar N repos: check repo a repo en vivo, no
   una barra opaca. Generalizar a todo lo que itera (arqueología por agente, sonda por MCP).
3. **Listas grandes necesitan filtro y selección visible.** Orgs con miles de repos: filtro de
   texto, contador "N de M cargados (hay más páginas)", selección persistente y visible aunque
   filtres. Generalizar como patrón de lista seleccionable.
4. **Formularios de entrevista con defaults recomendados y evidencia visible.** Cada campo
   pre-llenado muestra el *porqué* (sparkle + evidencia del discover). El usuario decide, la
   evidencia convence.
5. **El editor de clusters con grid de checkboxes era ilegible** (27 checkboxes por cluster).
   Ya se cambió a chips con × + dropdown para agregar. Valida y pule ese patrón chips-editor.
6. **Los pasos LLM deben "narrar":** qué modelo corre, qué herramienta usa, cuánto lleva
   gastado — no un spinner mudo durante 5 minutos.
7. **Cards MCP con estados honestos:** *identificado* (secreto guardado) vs *certificado*
   (el MCP contestó con él) y tools como chips toggleables. Pule la legibilidad de esa jerarquía.
8. **Estados vacíos que digan qué hacer** — cada `Empty` debe dar el siguiente paso concreto,
   no "no hay datos".
9. **Al terminar el wizard, el tránsito al panel debe ser obvio** — la pantalla Fin debe
   celebrar, resumir lo creado y llevar al Resumen sin ambigüedad.

---

## 7 · Qué debes producir (en este orden)

### A. Auditoría heurística pantalla por pantalla
Recorre el inventario del §5. Para cada pantalla: 3-6 hallazgos concretos (jerarquía, densidad,
consistencia, affordance, estados) clasificados **[crítico / mejora / pulido]**, citando el
elemento exacto. Usa heurísticas de Nielsen + los requisitos del §6 como lente.

### B. Sistema visual coherente (el "Agora 2.0")
- **Escala tipográfica cerrada**: hoy hay ~14 tamaños ad-hoc entre 8.5 y 30px. Propón una
  escala de 6-8 pasos con nombre, uso y clase Tailwind (p. ej. `text-xs`, arbitrarios solo
  donde se justifique), y el mapeo de cada primitivo de `bits.tsx` a ella. DM Sans para
  headings, Inter para body, mono para datos — define cuándo cada una.
- **Espaciado y densidad**: retícula de espaciado (gap/padding por nivel: página, sección,
  card, fila), y dos densidades si hace falta (observación vs formularios).
- **Color semántico**: consolida ok/wait/bad/brand — cuándo texto, cuándo fondo /8, cuándo
  borde /40, cuándo glow. Tabla de combinaciones permitidas y prohibidas.
- **Iconografía**: lucide — tamaño por contexto (3, 3.5, 4…), qué icono significa qué estado
  (hoy: CircleCheck/CircleX/Loader2/CircleDashed/CirclePause) — fija el vocabulario.
- **Motion sobrio**: qué se anima (spinner solo si corre, pulse solo si vivo, transiciones
  ≤150ms) y qué jamás (nada de typewriters, ni skeletons perpetuos). Respeta
  `prefers-reduced-motion` (ya hay precedente en keycaps/cursor).

### C. Rediseños concretos, priorizados — **wizard primero**
Orden: (1) StepShell + Stepper + LogTail + narración LLM, (2) pantallas Repositorios /
Discover / Agentes / MCPs, (3) Fin→panel, (4) Resumen, (5) Skills & MCP y Terminales,
(6) resto. Para cada rediseño entrega una **spec por componente**:

```
### <Componente/Pantalla>
Problema: <1-2 líneas, citando el hallazgo de la auditoría>
Antes:    <estructura y clases actuales relevantes>
Después:  <estructura JSX esquemática + clases Tailwind exactas>
Estados:  default / hover / focus-visible / running / error / empty / disabled
Temas:    diferencias dark OLED vs light (si las hay)
Por qué:  <1 línea>
```

### D. Accesibilidad
- Contraste AA mínimo en ambos temas (ojo: muted-foreground/50-60 sobre negro puro y los
  semánticos /8 de fondo — verifica los pares reales del §4).
- Focus visible en TODO lo clicable (hoy muchas cards-botón no declaran focus-visible).
- Navegación por teclado del wizard y de las listas seleccionables; roles/aria de los chips
  toggleables (el ToolPicker es un botón con line-through — propone semántica correcta).
- Textos de estado que no dependan solo del color (icono + palabra, ya es la norma: consérvala).

### E. Ambos temas, siempre
Cada spec debe funcionar en **dark True Black OLED (default)** y **light Enterprise Clean**.
Si propones tokens nuevos, dalos en OKLCH para ambos temas siguiendo el formato del §4.

### F. Respeto absoluto a las restricciones
Server-driven cada 2s (nada que exija streaming por tecla ni estado que el server no manda),
cero dependencias nuevas, honestidad visual, español, 127.0.0.1, secretos nunca en pantalla.
Si una idea viola algo de esto, descártala o márcala explícitamente como "requiere cambio de
backend" en una sección aparte de deuda/futuros.

---

## 8 · Formato de entrega

Un solo documento markdown con: resumen ejecutivo (10 líneas), auditoría (§A), sistema (§B),
specs priorizadas (§C, numeradas por prioridad de implementación), accesibilidad (§D) y una
lista final de "quick wins" (cambios de <10 líneas cada uno) para arrancar hoy. Las specs deben
poder implementarse una por una, en orden, sin romper las demás.

Empieza por la auditoría del wizard (Stepper + StepShell) y pregunta solo si un dato del
snapshot que necesitas no está descrito en este documento.
