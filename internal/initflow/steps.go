package initflow

// runnerFor devuelve la implementación de un paso, o nil si esa fase del
// wizard aún no existe en esta versión. Cada fase de desarrollo agrega su
// case; el dispatcher trata nil como "no implementado" con un error honesto.
func (m *Manager) runnerFor(id string) runner {
	switch id {
	case "clone":
		return (*Manager).runClone
	// F5: "requirements" · F6: "discover", "enrich"
	// F7/F8: "generate", "archaeology" · F9: "mcps" · F10: "first-task", "finish"
	default:
		return nil
	}
}
