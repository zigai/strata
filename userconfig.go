package strata

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/zigai/strata/internal/cascade"
)

var errNoEditPath = errors.New("no configuration file is selected for editing")

// UserConfigFile returns the existing user-tier config.toml, config.yaml,
// config.yml, or config.json for appName. If none exists, it returns the
// config.toml path in the same directory without creating the file.
func UserConfigFile(appName string) (string, error) {
	return cascade.UserConfigFile(appName)
}

// ConfigEditPath returns the file configuration edits should target. An
// explicit WithPath or WithOptionalPath wins; otherwise it selects the user
// file using the same format order and home-directory rules as loading. It
// returns an error when file loading is disabled or no app name is configured.
func ConfigEditPath(opts ...Option) (string, error) {
	config := defaultLoadOptions()
	for _, opt := range opts {
		opt(config)
	}

	if err := prepareRegistry(config); err != nil {
		return "", err
	}

	if config.explicitPath != "" {
		if config.explicitPath == "-" {
			return "", fmt.Errorf("%w: standard input cannot be edited", errNoEditPath)
		}

		if _, ok := config.codecReg.Get(filepath.Ext(config.explicitPath)); !ok {
			return "", fmt.Errorf("%w: %s", ErrUnsupportedFormat, config.explicitPath)
		}

		return config.explicitPath, nil
	}

	if config.withoutFiles || config.appName == "" {
		return "", errNoEditPath
	}

	return cascade.UserConfigFileForExtensions(config.appName, config.codecReg.Extensions())
}
