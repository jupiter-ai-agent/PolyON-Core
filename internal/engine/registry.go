package engine

// Registry holds all registered Engine adapters.
type Registry struct {
	engines map[string]Engine
}

// NewRegistry creates a new, empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		engines: make(map[string]Engine),
	}
}

// Register adds an Engine to the registry keyed by its name.
func (r *Registry) Register(e Engine) {
	r.engines[e.Name()] = e
}

// Get returns an Engine by name.
func (r *Registry) Get(name string) (Engine, bool) {
	e, ok := r.engines[name]
	return e, ok
}

// Health returns a map of engine name → HealthStatus for all registered engines.
func (r *Registry) Health() map[string]HealthStatus {
	result := make(map[string]HealthStatus, len(r.engines))
	for name, e := range r.engines {
		result[name] = e.Health()
	}
	return result
}
