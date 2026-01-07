package runner

import (
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// createProviderConfigYAML marshals the input map to the given location on the disk
func createProviderConfigYAML(configFilePath string) error {
	// Define the full path for the file
	dir := filepath.Dir(configFilePath)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		// Recursively create all necessary directories
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	configFile, err := os.Create(configFilePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := configFile.Close(); err != nil {
			log.Printf("Error closing config file: %s", err)
		}
	}()

	sourcesRequiringApiKeysMap := make(map[string][]string)
	for _, source := range AllSources {
		if source.NeedsKey() {
			sourceName := strings.ToLower(source.Name())
			sourcesRequiringApiKeysMap[sourceName] = []string{}
		}
	}

	return yaml.NewEncoder(configFile).Encode(sourcesRequiringApiKeysMap)
}

// UnmarshalFrom writes the marshaled yaml config to disk
func UnmarshalFrom(file string) error {
	reader, err := os.Open(file)
	if err != nil {
		return err
	}

	sourceApiKeysMap := map[string][]string{}
	err = yaml.NewDecoder(reader).Decode(sourceApiKeysMap)
	for _, source := range AllSources {
		sourceName := strings.ToLower(source.Name())
		apiKeys := sourceApiKeysMap[sourceName]
		if len(apiKeys) > 0 {
			source.AddApiKeys(apiKeys)
		}
	}
	return err
}
