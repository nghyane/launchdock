package config

import (
	"os"
	"reflect"
	"testing"
)

func TestCanonicalRoutingConfigParse(t *testing.T) {
	yamlContent := `
port: 8080
routing:
  canonical-models-only: true
  canonical-model-source: "fallbacks"
  canonical-models-include:
    - "bonus-model"
  hide-provider-models: true
`
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(yamlContent)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	cfg, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to parse config: %v", err)
	}

	if !cfg.Routing.CanonicalModelsOnly {
		t.Errorf("expected canonical-models-only to be true")
	}
	if cfg.Routing.CanonicalModelSource != "fallbacks" {
		t.Errorf("expected canonical-model-source to be 'fallbacks', got '%s'", cfg.Routing.CanonicalModelSource)
	}
	if cfg.Routing.HideProviderModels != true {
		t.Errorf("expected hide-provider-models to be true")
	}

	expectedInclude := []string{"bonus-model"}
	if !reflect.DeepEqual(cfg.Routing.CanonicalModelsInclude, expectedInclude) {
		t.Errorf("expected includes %v, got %v", expectedInclude, cfg.Routing.CanonicalModelsInclude)
	}
}
