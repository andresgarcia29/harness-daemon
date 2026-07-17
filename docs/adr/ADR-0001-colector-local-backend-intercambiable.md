# ADR-0001 — El colector es siempre local; el backend es intercambiable

`status: ACCEPTED` · 2026-07-16

## Contexto

Queremos ver el trabajo de los agentes desde el navegador, en la máquina local
y también centralizado en la nube (un Mac + un VPC Linux + CronJobs de K8s,
varios clientes). La tentación es "un daemon local" vs "un daemon en la nube".

## Decisión

**No existe "el mismo daemon pero en la nube".** Todo lo que observamos —
`tasks/`, `.harness/events.jsonl`, el estado de git, los transcripts — vive en
un **sistema de archivos local**. Un proceso remoto no puede verlo. Así que:

```
colector (SIEMPRE local)  →  reporta a  →  almacén + UI (local U remoto)
```

Un binario, tres modos:

| modo         | qué corre                        | dónde miras          |
|--------------|----------------------------------|----------------------|
| `all-in-one` | colector + almacén + UI          | `127.0.0.1:7718`     |
| `collect`    | solo colector → sink remoto      | — (VPC, CronJob K8s) |
| `serve`      | recibe + almacena + sirve la UI  | una URL              |

El sink es una interfaz con dos implementaciones (`LocalSQLite`, `RemoteHTTP`).

**El sink es POR WORKSPACE, no por máquina.** Una laptop trabaja para varios
clientes; cada workspace reporta al servidor de SU cliente, y uno puede
quedarse local mientras los otros suben. El destino vive en
`harness-answers.yaml → daemon.endpoint`, junto al resto de decisiones de ese
workspace.

## Consecuencias

- La nube deja de ser un rediseño y pasa a ser una implementación de `Sink`.
- Lo que corre en Kubernetes es `serve`: **recibe**, no observa. En el clúster
  no hay colector y no hay agente.
- Los CronJobs de K8s del harness (ci-doctor, vuln-watch, rule-miner…) que hoy
  corren ciegos pasan a ser "otra máquina que reporta", gratis.
- Hay que construir la costura desde el día 1 aunque no usemos la nube: `Sink`
  como interfaz, `machine_id`, workspace-por-remote. Retrofitear eso es caro;
  el servidor en sí es una tarde.

## Alternativas rechazadas

- **Montar el FS remoto (sshfs/NFS)** para que un daemon central observe: latencia,
  fragilidad, y SQLite sobre NFS tiene el locking roto. No.
- **Que el agente reporte directo a la nube sin colector local**: obliga a cada
  CLI a saber de nosotros. El colector existe justo para que no tengan que saberlo.
- **Antes de construir la nube: `ssh -L 7718:localhost:7718 vpc`** te da el
  panel del VPC hoy, con cero infraestructura. Con 1–3 máquinas, el túnel gana.
  La nube se justifica con muchas máquinas o con gente sin SSH (un PM, un cliente).
