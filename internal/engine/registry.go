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
// Health checks run in parallel to avoid sequential timeout accumulation.
func (r *Registry) Health() map[string]HealthStatus {
	type kv struct {
		name   string
		status HealthStatus
	}
	ch := make(chan kv, len(r.engines))
	for name, e := range r.engines {
		go func(n string, eng Engine) {
			ch <- kv{n, eng.Health()}
		}(name, e)
	}
	result := make(map[string]HealthStatus, len(r.engines))
	for range r.engines {
		pair := <-ch
		result[pair.name] = pair.status
	}
	return result
}
