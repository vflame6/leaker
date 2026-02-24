package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCreateProviderConfigYAML_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider-config.yaml")

	if err := createProviderConfigYAML(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read created config: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty config file")
	}
}

func TestCreateProviderConfigYAML_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "provider-config.yaml")

	if err := createProviderConfigYAML(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected config file to exist after creating parent dirs")
	}
}

func TestCreateProviderConfigYAML_ContainsSourcesNeedingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider-config.yaml")

	if err := createProviderConfigYAML(path); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// At least one source in AllSources requires a key (LeakCheck does).
	// The YAML should contain that source name.
	foundAny := false
	for _, source := range AllSources {
		if source.NeedsKey() {
			if strings.Contains(content, strings.ToLower(source.Name())) {
				foundAny = true
				break
			}
		}
	}
	if !foundAny {
		t.Errorf("expected at least one key-requiring source in config YAML, got:\n%s", content)
	}
}

func TestUnmarshalFrom_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Write a config with a fake key for leakcheck
	cfg := map[string][]string{"leakcheck": {"fakekey123"}}
	data, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	// UnmarshalFrom should not error on a valid file
	if err := UnmarshalFrom(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnmarshalFrom_MissingFile(t *testing.T) {
	err := UnmarshalFrom("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestUnmarshalFrom_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("not: valid: yaml: [unclosed"), 0644); err != nil {
		t.Fatal(err)
	}

	err := UnmarshalFrom(path)
	if err == nil {
		t.Error("expected error for malformed YAML")
	}
}
