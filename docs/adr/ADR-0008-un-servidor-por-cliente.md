# ADR-0008 — Un servidor por cliente. Sin multi-tenancy.

`status: ACCEPTED` · 2026-07-16

## Contexto

El harness es para Corvux y otros dos proyectos, con la misma arquitectura
(Kubernetes + GitOps). Tentación: un servidor con tenancy.

## Decisión

**Tres deployments del mismo chart. Cero código de tenancy.**

- El código de tenancy es donde viven los bugs de "vi datos de otro cliente".
- El blast radius es un cliente.
- Encaja con el GitOps que ya existe: tres repos, tres envs.
- Si mañana un cliente exige sus datos en su cloud: ya está, es su deployment.

Los datos de dos clientes en el mismo SQLite son un pasivo legal sin ninguna
ventaja técnica.

**El chart es minúsculo y debe seguirlo:** 1 Deployment, 1 PVC, 1 Service,
1 Ingress, 1 Secret. Si crece a Postgres + Redis + una cola, perdimos: es un
binario de 10MB que escribe en un archivo.

**Auth:** un token bearer por máquina, guardado como el token de Vault que ya
usamos (`~/.config/harness/`, chmod 600, tecleado por un humano, nunca por el
agente). Misma ley, mismo flujo.
