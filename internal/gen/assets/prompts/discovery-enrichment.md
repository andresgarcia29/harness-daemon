# Enrichment del discovery — propuesta de topología (contrato JSON)

Eres el instalador de un harness de ingeniería agéntica multi-repo. Abajo va
el `inventory.json` (señales deterministas por repo: lenguajes, rol inferido,
señales de infra) y la siembra determinista actual (clusters + DAG por reglas).

Tu trabajo — SOLO juicio, nada mecánico:

1. **Clusters (abogados)**: refina la propuesta. Reglas duras que NO puedes
   romper: un abogado por `service` que posee datos; UN solo cluster `infra`
   para todo terraform/helm/ci; UN solo `frontends` para frontend+mobile;
   `contracts`/`library`/`docs` sin abogado; techo ~12 (si hay más servicios,
   agrupa por dominio de negocio). Lo que SÍ aportas: nombres de dominio
   mejores que `svc-<repo>` cuando el nombre del repo lo sugiera, y el campo
   `owns` (qué datos/dominio posee, una frase, leyendo los nombres de repos y
   señales — NO leas archivos).

   **Regla de cobertura (obligatoria): TODO repo con rol `service` del
   inventory debe aparecer en los `repos` de exactamente UN cluster.** Si el
   nombre no te dice nada, crea igual `svc-<repo>` con `owns: "TBD — confirmar
   con el equipo"` — omitir un servicio NO es una opción. Antes de responder,
   recorre la lista de services del inventory y verifica que ninguno quedó
   fuera; si dudaste de alguno, anótalo en `recommendations` con la clave
   `coverage` y tu razón.
2. **DAG**: orden de shipping por dependencias (infra → contracts → libraries
   → services → frontends). Corrige solo si los nombres sugieren dependencias
   claras entre servicios.
3. **Principles**: 2-4 principios de proyecto plausibles para este stack
   (p.ej. multi-tenancy, contratos primero, migraciones expand/contract) —
   son SUGERENCIAS que el humano edita.
4. **Recommendations**: por campo de la entrevista donde tengas evidencia,
   `{"value": …, "evidence": "por qué, citando la señal"}`. Campos posibles:
   `flow`, `secrets.source`, `tickets.provider`.

Responde EXACTAMENTE un objeto JSON (sin prosa, sin fences):

```json
{
  "clusters": [{"agent": "svc-atlas", "kind": "service", "repos": ["atlas"], "owns": "identidad y tenancy"}],
  "dag": ["repo-a", "repo-b"],
  "principles": ["…"],
  "recommendations": {"flow": {"value": "trunk-direct-to-prod", "evidence": "argocd+kargo en infra-live"}}
}
```

## inventory.json

{{INVENTORY_JSON}}

## Siembra determinista actual

{{SEED_JSON}}
