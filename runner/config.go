package runner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"github.com/vflame6/leaker/utils"
	"gopkg.in/yaml.v3"
)

// createProviderConfigYAML marshals the input map to the given location on the disk
func createProviderConfigYAML(configFilePath string) error {
	// Define the full path for the file
	dir := filepath.Dir(configFilePath)

	// The file holds API keys: only the user may read it.
	if err := os.MkdirAll(dir, utils.PrivateDirMode); err != nil {
		return err
	}

	configFile, err := os.OpenFile(configFilePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, utils.PrivateFileMode)
	if err != nil {
		return err
	}
	defer func() {
		if err := configFile.Close(); err != nil {
			logger.Errorf("Error closing config file: %s", err)
		}
	}()

	sourcesRequiringApiKeysMap := make(map[string][]string)
	for _, source := range AllSources {
		if source.UsesKey() {
			sourceName := strings.ToLower(source.Name())
			sourcesRequiringApiKeysMap[sourceName] = []string{}
		}
	}

	return yaml.NewEncoder(configFile).Encode(sourcesRequiringApiKeysMap)
}

// loadProviderKeys reads the provider config at path and gives each source in
// srcs the keys listed under its name.
func loadProviderKeys(path string, srcs []sources.Source) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	keys := map[string][]string{}
	if err := yaml.Unmarshal(data, &keys); err != nil {
		return err
	}
	for _, source := range srcs {
		name := strings.ToLower(source.Name())
		// Blank entries are placeholders, not keys.
		var apiKeys []string
		for _, key := range keys[name] {
			if key = strings.TrimSpace(key); key != "" {
				apiKeys = append(apiKeys, key)
			}
		}
		if len(apiKeys) > 0 {
			logger.Debugf("API key(s) found for %s.", name)
			source.AddApiKeys(apiKeys)
		}
	}
	return nil
}
