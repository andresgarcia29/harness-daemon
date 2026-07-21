// Package contract publica el contrato de lectura daemon→UI. El artefacto TS
// (harness.gen.ts) se genera desde los structs Go de internal/api — ellos son
// la fuente de verdad. La UI lo consume por codegen, de modo que un cambio de
// forma en el daemon se vuelve un error de build en la UI (ADR-0003, workspace
// corvux). Config en tygo.yaml.
//
//go:generate go tool tygo generate
package contract
