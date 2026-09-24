package strata

import (
	"errors"
	"fmt"

	"github.com/zigai/strata/internal/cascade"
)

var errNoEditPath = errors.New("no configuration file is selected for editing")

// ConfigEditPath returns the file configuration edits should target. An
// explicit WithPath or WithOptionalPath wins; otherwise it selects the user
// file using the same format order and home-directory rules as loading. It
// returns an error when file loading is disabled or no app name is configured.
func ConfigEditPath(opts ...Option) (string, error) {
	config := defaultLoadOptions()
	for _, opt := range opts {
		opt(config)
	}

	if config.explicitPath == "-" {
		return "", fmt.Errorf("%w: standard input cannot be edited", errNoEditPath)
	}

	if err := prepareLoadOptions(config); err != nil {
		return "", err
	}

	if config.explicitPath != "" {
		return config.explicitPath, nil
	}

	if config.withoutFiles || config.appName == "" {
		return "", errNoEditPath
	}

	return cascade.UserConfigFileForExtensions(config.appName, config.codecReg.Extensions())
}
