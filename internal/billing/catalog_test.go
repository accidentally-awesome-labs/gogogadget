package billing

import "testing"

func TestPlanCatalogDeepCopies(t *testing.T) {
	c, err := NewPlanCatalog([]Plan{{Key: "free", Features: []string{"base"}}, {Key: "pro", ProviderProductID: "prod_1", Meters: []Meter{{Key: "m"}}}})
	if err != nil {
		t.Fatal(err)
	}
	p := c.ByKey("pro")
	p.Meters[0].Key = "mutated"
	all := c.All()
	all[0].Features[0] = "mutated"
	if got := c.ByKey("pro").Meters[0].Key; got != "m" {
		t.Fatalf("meter leaked: %q", got)
	}
	if got := c.ByKey("unknown").Key; got != "free" {
		t.Fatalf("fallback=%q", got)
	}
	if _, ok := c.ByProviderProductID("missing"); ok {
		t.Fatal("unknown product resolved")
	}
}
