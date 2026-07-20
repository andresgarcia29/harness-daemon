# Evidence v1

`ship.sh` no acepta un “pass” narrativo. Cada prueba que sustenta el veredicto
se ejecuta con el runner:

```bash
scripts/evidence.py run \
  --task-dir tasks/<task-id> \
  --repo <repo> \
  --runner <identidad> \
  --kind test \
  --cwd worktrees/<task-id>/<repo> \
  -- <comando de prueba>
```

El runner conserva el output y un manifiesto JSON con task, repo, identidad,
comando, commit antes/después, exit code y SHA-256. El ID impreso se agrega a
`verdict-<repo>.json` bajo `evidence[]`.

Durante ship, `evidence.py verify` exige que cada ID:

- pertenezca a esta tarea y repositorio;
- haya terminado en cero;
- pertenezca exactamente al HEAD que se publicará;
- conserve el output original y su hash;
- cubra los tipos de evidencia requeridos por policy.

Editar un manifiesto, copiar evidencia de otra tarea o cambiar código después
de probar invalida el gate. Los manifiestos usan `schema: 1`; cualquier cambio
incompatible requiere una nueva versión de schema.
