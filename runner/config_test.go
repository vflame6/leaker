package runner

import (
	"errors"
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
		if source.UsesKey() {
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

func writeProviderConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadProviderKeys_GivesKeysToTheRunnersSources(t *testing.T) {
	data, _ := yaml.Marshal(map[string][]string{"leakcheck": {"fakekey123"}})
	path := writeProviderConfig(t, string(data))

	srcs := NewSources()
	if err := loadProviderKeys(path, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range srcs {
		if got, want := s.HasKeys(), s.Name() == "leakcheck"; got != want {
			t.Errorf("%s.HasKeys() = %v, want %v", s.Name(), got, want)
		}
	}
	for _, s := range AllSources {
		if s.HasKeys() {
			t.Errorf("global AllSources must stay key-free, %s has keys", s.Name())
		}
	}
}

func TestLoadProviderKeys_EmptyFileIsFine(t *testing.T) {
	if err := loadProviderKeys(writeProviderConfig(t, ""), NewSources()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadProviderKeys_MissingFile(t *testing.T) {
	if err := loadProviderKeys("/nonexistent/path/config.yaml", NewSources()); err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestLoadProviderKeys_MalformedYAML(t *testing.T) {
	if err := loadProviderKeys(writeProviderConfig(t, "not: valid: yaml: [unclosed"), NewSources()); err == nil {
		t.Error("expected error for malformed YAML")
	}
}

// TestNewRunner_ProviderConfigProblemsAreConfigErrors: a -p file that is
// missing or unreadable stops the run instead of silently using no keys.
func TestNewRunner_ProviderConfigProblemsAreConfigErrors(t *testing.T) {
	captureLogs(t)
	for name, path := range map[string]string{
		"missing":   filepath.Join(t.TempDir(), "nope.yaml"),
		"malformed": writeProviderConfig(t, "not: valid: yaml: [unclosed"),
	} {
		_, err := NewRunner(&Options{ProviderConfig: path, Sources: []string{"proxynova"}, NoWriteDB: true, DBPath: filepath.Join(t.TempDir(), "x.db")})
		var cfgErr *ConfigError
		if !errors.As(err, &cfgErr) {
			t.Errorf("%s config: err = %v, want *ConfigError", name, err)
		}
	}
}

// TestNewRunner_BlankKeyIsNoKey: a placeholder empty key does not make a
// source runnable.
func TestNewRunner_BlankKeyIsNoKey(t *testing.T) {
	captureLogs(t)
	config := writeProviderConfig(t, "snusbase: ['', '   ']\n")
	_, err := NewRunner(&Options{ProviderConfig: config, Sources: []string{"snusbase"}, NoWriteDB: true,
		DBPath: filepath.Join(t.TempDir(), "x.db")})
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "snusbase") {
		t.Fatalf("err = %v, want a *ConfigError naming snusbase", err)
	}
}
