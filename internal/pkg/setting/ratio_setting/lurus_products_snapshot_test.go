package ratio_setting

import "testing"

// lurusProductIDs is a snapshot of products[].id in lurus-platform
// api/ontology.yaml (ADR 0034) at 13a60675. That file is the one place a
// product ID is defined; the wallet rejects any other id as unknown once
// billing.require_product_attribution is on. Product IDs are append-only, so
// a stale snapshot can only miss a new product, never accept a retired one:
// when onboarding a product to the hub, copy its id from the platform file.
var lurusProductIDs = map[string]bool{
	"llm-api": true, "tally": true, "lucrum": true, "switch": true,
	"creator": true, "lutu": true, "kova": true, "forge": true,
	"lumen": true, "memorus": true, "private-ai-suite": true, "lumao": true,
	"quant-trading": true, "webmail": true,
}

func TestAllowedSourceProductsAreLurusProducts(t *testing.T) {
	for p := range allowedSourceProducts {
		if !lurusProductIDs[p] {
			t.Errorf("allowedSourceProducts has %q, which is not a lurus-platform product id (api/ontology.yaml)", p)
		}
	}
	if !lurusProductIDs[DefaultSourceProduct] {
		t.Errorf("DefaultSourceProduct %q is not a lurus-platform product id", DefaultSourceProduct)
	}
}
