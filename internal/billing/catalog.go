package billing

import "errors"

type PlanCatalog interface {
	All() []Plan
	ByKey(string) Plan
	ByProviderProductID(string) (Plan, bool)
}
type planCatalog struct {
	plans     []Plan
	byKey     map[string]Plan
	byProduct map[string]Plan
}

func clonePlan(p Plan) Plan {
	p.Meters = append([]Meter(nil), p.Meters...)
	p.Features = append([]string(nil), p.Features...)
	return p
}
func NewPlanCatalog(plans []Plan) (PlanCatalog, error) {
	if len(plans) == 0 {
		return nil, errors.New("billing: plan catalog cannot be empty")
	}
	c := &planCatalog{plans: make([]Plan, len(plans)), byKey: make(map[string]Plan, len(plans)), byProduct: make(map[string]Plan)}
	for i, p := range plans {
		if p.Key == "" {
			return nil, errors.New("billing: plan key cannot be empty")
		}
		if _, ok := c.byKey[p.Key]; ok {
			return nil, errors.New("billing: duplicate plan key " + p.Key)
		}
		p = clonePlan(p)
		c.plans[i] = p
		c.byKey[p.Key] = p
		if p.ProviderProductID != "" {
			if _, ok := c.byProduct[p.ProviderProductID]; ok {
				return nil, errors.New("billing: duplicate provider product id")
			}
			c.byProduct[p.ProviderProductID] = p
		}
	}
	return c, nil
}
func (c *planCatalog) All() []Plan {
	out := make([]Plan, len(c.plans))
	for i, p := range c.plans {
		out[i] = clonePlan(p)
	}
	return out
}
func (c *planCatalog) ByKey(k string) Plan {
	if p, ok := c.byKey[k]; ok {
		return clonePlan(p)
	}
	return clonePlan(c.byKey["free"])
}
func (c *planCatalog) ByProviderProductID(id string) (Plan, bool) {
	p, ok := c.byProduct[id]
	if !ok {
		return Plan{}, false
	}
	return clonePlan(p), true
}
