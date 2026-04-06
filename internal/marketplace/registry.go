package marketplace

type Registry struct {
	marketplaces map[string]Marketplace
	messengers   map[string]Messenger
}

func NewRegistry() *Registry {
	return &Registry{
		marketplaces: map[string]Marketplace{},
		messengers:   map[string]Messenger{},
	}
}

func (r *Registry) Register(m Marketplace) {
	if r == nil || m == nil {
		return
	}
	r.marketplaces[m.ID()] = m
	if messenger, ok := m.(Messenger); ok {
		r.messengers[m.ID()] = messenger
	}
}

func (r *Registry) Get(id string) (Marketplace, bool) {
	if r == nil {
		return nil, false
	}
	m, ok := r.marketplaces[id]
	return m, ok
}

func (r *Registry) Messenger(id string) (Messenger, bool) {
	if r == nil {
		return nil, false
	}
	m, ok := r.messengers[id]
	return m, ok
}

func (r *Registry) All() []Marketplace {
	if r == nil {
		return nil
	}
	out := make([]Marketplace, 0, len(r.marketplaces))
	for _, marketplace := range r.marketplaces {
		out = append(out, marketplace)
	}
	return out
}
