package strata

import (
	"errors"
	"fmt"

	"github.com/zigai/strata/internal/cascade"
)

var errNoEditPath = errors.New("no configuration file is selected for editing")

// EditPath returns the file configuration edits should target. An
// explicit WithPath or WithOptionalPath wins; otherwise it selects the user
// file using the same format order and home-directory rules as loading. It
// returns an error when file loading is disabled or no app name is configured.
func EditPath(opts ...Option) (string, error) {
	options := defaultLoadOptions()
	for _, opt := range opts {
		opt(options)
	}

	if options.explicitPath == "-" {
		return "", fmt.Errorf("%w: standard input cannot be edited", errNoEditPath)
	}

	if err := prepareLoadOptions(options); err != nil {
		return "", err
	}

	if options.explicitPath != "" {
		return options.explicitPath, nil
	}

	if options.withoutFileDiscovery || options.appName == "" {
		return "", errNoEditPath
	}

	return cascade.UserConfigFileForExtensions(options.appName, options.codecs.Extensions())
}
