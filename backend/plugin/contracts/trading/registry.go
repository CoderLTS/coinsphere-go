package trading

import (
	"fmt"
	"sort"
)

// Registry is wired by the application composition root, outside the generic SDK.
type Registry struct {
	marketData map[string]MarketDataProvider
	execution  map[string]ExecutionProvider
	strategies map[string]Strategy
}

func NewRegistry() *Registry {
	return &Registry{marketData: map[string]MarketDataProvider{}, execution: map[string]ExecutionProvider{}, strategies: map[string]Strategy{}}
}
func (r *Registry) RegisterMarketData(p MarketDataProvider) error {
	if p == nil || p.ID() == "" {
		return fmt.Errorf("market data provider requires an ID")
	}
	if r.marketData[p.ID()] != nil {
		return fmt.Errorf("duplicate market data provider %s", p.ID())
	}
	r.marketData[p.ID()] = p
	return nil
}
func (r *Registry) RegisterExecution(p ExecutionProvider) error {
	if p == nil || p.ID() == "" {
		return fmt.Errorf("execution provider requires an ID")
	}
	if r.execution[p.ID()] != nil {
		return fmt.Errorf("duplicate execution provider %s", p.ID())
	}
	r.execution[p.ID()] = p
	return nil
}
func (r *Registry) RegisterStrategy(p Strategy) error {
	if p == nil || p.Descriptor().ID == "" {
		return fmt.Errorf("strategy requires an ID")
	}
	if r.strategies[p.Descriptor().ID] != nil {
		return fmt.Errorf("duplicate strategy %s", p.Descriptor().ID)
	}
	r.strategies[p.Descriptor().ID] = p
	return nil
}
func (r *Registry) MarketDataProvider(id string) (MarketDataProvider, bool) {
	p, ok := r.marketData[id]
	return p, ok
}
func (r *Registry) ExecutionProvider(id string) (ExecutionProvider, bool) {
	p, ok := r.execution[id]
	return p, ok
}
func (r *Registry) Strategy(id string) (StrategyDescriptor, Strategy, bool) {
	p, ok := r.strategies[id]
	if !ok {
		return StrategyDescriptor{}, nil, false
	}
	return p.Descriptor(), p, true
}
func (r *Registry) Strategies() []StrategyDescriptor {
	items := []StrategyDescriptor{}
	for _, p := range r.strategies {
		items = append(items, p.Descriptor())
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}
