package config

import "testing"

// TestLoadConfigExample ensures the provided example config parses and validates.
func TestLoadConfigExample(t *testing.T) {
	t.Parallel()

	if _, err := Load("../config.example.json"); err != nil {
		t.Fatalf("Load(config.example.json) returned error: %v", err)
	}
}
