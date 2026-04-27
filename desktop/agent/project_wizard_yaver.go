package main

// writeYaverProjectConfig emits .yaver/config.yaml + .yaver/services.yaml for a
// freshly scaffolded project. The wizard answers decide which backend + services
// get wired up.
func writeYaverProjectConfig(dir string, a map[string]string) error {
	backend := mapWizardBackend(a)
	cfg := &YaverProjectConfig{
		Backend: backend,
		Stack:   a["stack"],
		Auth:    a["auth"],
		Env:     map[string]string{},
	}
	if backend == "" {
		// No explicit backend — skip; adapter layer will infer if needed.
		return SaveProjectConfig(dir, cfg)
	}
	if err := SaveProjectConfig(dir, cfg); err != nil {
		return err
	}

	// Wire default services based on backend choice. The user can edit
	// .yaver/services.yaml afterwards with `yaver services add`.
	sm := NewServicesManager(dir)
	for _, name := range servicesFor(backend, a) {
		if _, err := sm.Add(name, nil); err != nil {
			_ = err // soft-fail: preset may not exist for custom choices
		}
	}
	return nil
}

func mapWizardBackend(a map[string]string) BackendKind {
	switch a["backend"] {
	case "convex":
		return BackendConvex
	case "supabase":
		return BackendSupabase
	case "postgres", "pg":
		return BackendPostgres
	case "sqlite":
		return BackendSQLite
	}
	// Fall back to the `db` answer if the wizard has one.
	switch a["db"] {
	case "convex":
		return BackendConvex
	case "supabase":
		return BackendSupabase
	case "postgres":
		return BackendPostgres
	case "sqlite":
		return BackendSQLite
	}
	return ""
}

// servicesFor returns the default service preset names for a given backend.
// These match presets in services.go.
func servicesFor(backend BackendKind, a map[string]string) []string {
	var out []string
	switch backend {
	case BackendConvex:
		out = append(out, "convex", "convex-dashboard")
	case BackendSupabase:
		// Supabase is launched via its own CLI (`supabase start`) — no preset.
	case BackendPostgres:
		out = append(out, "postgres")
	}
	return out
}
