# ¿Dónde está el esquema?

En [`internal/store/migrations/`](../../internal/store/migrations/) — Go solo
puede embeber (`go:embed`) archivos DENTRO del paquete, y dos copias del único
archivo caro de cambiar del proyecto era una divergencia esperando fecha.

Las leyes del esquema (medido vs. inferido, dedupe como restricción, identidad
≠ ruta) están comentadas en el propio `001_init.sql`. Migraciones: forward-only,
aditivas, `NNN_nombre.sql`; el daemon respalda `harness.db.bak-<NNN>` antes de
aplicar cada una.
