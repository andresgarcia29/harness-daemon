# ADR-0006 — Auto-update: firmado, verificado, y supervisado por bash

`status: ACCEPTED` · 2026-07-16

## Contexto

El binario debe actualizarse cuando queramos y relanzarse con la versión nueva,
en las laptops del equipo y en el VPC.

## Decisión

**Secuencia (cada paso existe por una trampa real):**

1. Descargar a un temp **en el mismo filesystem**.
   *No puedes sobrescribir un binario en ejecución: Linux da `ETXTBSY`; macOS te
   deja y corrompe el proceso vivo.*
2. **Verificar firma minisign** con la pubkey **compilada dentro del binario**.
3. `tmpbinary --selftest` — arranca, imprime versión, abre una DB de mentira,
   sale 0. *Atrapa arquitectura equivocada, descarga corrupta, glibc vieja.*
4. `rename()` sobre el destino (atómico en POSIX). El viejo se guarda como
   `harnessd.prev`. *El proceso vivo conserva su inode hasta morir.*
5. Reiniciar. Si `/health` no responde en 5s → **revertir a `.prev`** y
   reintentar una vez.

**El supervisor es bash, no Go.** El wrapper `harnessd ensure` (un script del
plugin) arranca y verifica. Si el binario nuevo revienta al arrancar, un
supervisor escrito en Go moriría con él y el siguiente `ensure` relanzaría el
mismo binario roto: **bucle infinito sin daemon y sin forma de recuperarlo.**
Bash es lo único que un mal build de Go no puede romper. Es la misma filosofía
que los hooks fail-closed: la última defensa no puede depender de lo que falla.

**Quién decide:** el plugin **pinnea** la versión (`daemon.lock`: version +
sha256 + firma). `make init` reconcilia. Actualizar a todo el equipo = sacar
release + bumpear el pin. Nadie se actualiza en silencio por su cuenta.

## Seguridad (no negociable)

Estamos poniendo **un binario que se auto-actualiza desde la red en la máquina
de cada dev**. Es exactamente la forma de `postmark-mcp` (15 releases limpias,
luego un backdoor) y de la cadena de `s1ngularity` (malware que invocó
`claude --dangerously-skip-permissions` para cosechar credenciales; 5.500 repos
privados hechos públicos).

- **Firma obligatoria.** Un checksum solo no vale: quien te sirve el binario te
  sirve el checksum.
- **El sha256 del primer binario va pinneado en el plugin**; el bootstrap no
  confía en la red, confía en el pin.
- **Nunca update silencioso en background.**
- **Ley del daemon: OBSERVA, NO EJECUTA.** Nunca corre código del repo, nunca
  lanza agentes. Un proceso que no puede ejecutar nada arbitrario es un objetivo
  mucho menos apetecible.

## Consecuencias

- El plugin pasa a distribuir artefactos compilados (binarios por plataforma,
  checksums, releases). Es un salto de mantenimiento real, y es para siempre.
- Migraciones forward-only + backup antes de migrar: revertir el binario no
  puede costarte los datos (ADR-0003).
