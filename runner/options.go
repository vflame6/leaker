package runner

import (
	"github.com/vflame6/leaker/utils"
	"path/filepath"
)

var (
	configDir             = utils.AppConfigDirOrDefault(".", "leaker")
	defaultConfigLocation = utils.GetEnvOrDefault("LEAKER_PROVIDER_CONFIG", filepath.Join(configDir, "provider-config.yaml"))
)

type Options struct {
	Targets string
}

//func (o *Options) loadProvidersFrom(filename string) {
//
//}
