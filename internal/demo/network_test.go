package demo

import (
	"testing"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// Les poids du YAML des TraefikService (3 et 1, miroir 10 %) donnent les parts
// publiées sur les règles de checkout (750/250, miroir 10).
func TestTraefikWeightsMatchStream(t *testing.T) {
	split := traefikServices["production/checkout-split"]["weighted"].(map[string]any)["services"].([]any)
	var total float64
	for _, x := range split {
		total += float64(x.(map[string]any)["weight"].(int))
	}
	// checkout-split : api-gateway (3) et checkout-mirror (1, redirigé vers orders-service).
	want := map[string]int{"api-gateway": int(float64(split[0].(map[string]any)["weight"].(int)) / total * 1000),
		"orders-service": int(float64(split[1].(map[string]any)["weight"].(int)) / total * 1000)}
	mirror := traefikServices["production/checkout-mirror"]["mirroring"].(map[string]any)["mirrors"].([]any)[0].(map[string]any)
	for _, r := range baseRoutes {
		if r.Name != "checkout" || r.Source != model.SourceIngressRoute {
			continue
		}
		for _, rule := range r.Rules {
			b := rule.Backend
			if b.Mirror {
				if b.Service != mirror["name"] || b.Percent != mirror["percent"] {
					t.Errorf("miroir publié %s %d%%, YAML %v %v%%", b.Service, b.Percent, mirror["name"], mirror["percent"])
				}
				continue
			}
			if b.Weight == nil || *b.Weight != want[b.Service] {
				t.Errorf("%s : poids publié %v, attendu %d", b.Service, b.Weight, want[b.Service])
			}
		}
	}
}
