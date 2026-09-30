package cascade

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zigai/strata/internal/stream"
)

const (
	SourceSystem = "system"

	SourceUser = "user"

	SourceFile = "file"

	SourceStdin = "stdin"
)

var ErrPathIsDirectory = errors.New("explicit configuration path is a directory")

var (
	errNoConfigFormat = errors.New("no configuration format is enabled")
	errNoHome         = errors.New("home directory is not available")
)

type Layer struct {
	Source string
	Path   string
	// Data holds stdin bytes and is nil for file layers, which the caller reads from Path.
	Data []byte
}

type DiscoverOptions struct {
	AppName       string
	ExplicitPath  string
	OptionalPath  bool
	SkipDiscovery bool
	StdinReader   io.Reader
	MaxFileSize   int64
	Extensions    []string
}

func Discover(opts DiscoverOptions) ([]Layer, error) {
	if opts.SkipDiscovery {
		if opts.ExplicitPath != "" {
			return discoverExplicitLayer(opts)
		}

		return nil, nil
	}

	var layers []Layer

	if opts.AppName != "" {
		if sysPath := discoverSystemLayer(opts.AppName, opts.Extensions); sysPath != "" {
			layers = append(layers, Layer{
				Source: SourceSystem,
				Path:   sysPath,
				Data:   nil,
			})
		}

		if userPath := discoverUserLayer(opts.AppName, opts.Extensions); userPath != "" {
			layers = append(layers, Layer{
				Source: SourceUser,
				Path:   userPath,
				Data:   nil,
			})
		}
	}

	if opts.ExplicitPath != "" {
		explicitLayers, err := discoverExplicitLayer(opts)
		if err != nil {
			return nil, err
		}

		layers = append(layers, explicitLayers...)
	}

	return layers, nil
}

func UserConfigFileForExtensions(appName string, exts []string) (string, error) {
	if len(exts) == 0 {
		return "", errNoConfigFormat
	}

	base, err := userConfigBase()
	if err != nil {
		return "", err
	}

	dir := filepath.Join(base, appName)
	if found := findConfigFile(dir, exts); found != "" {
		return found, nil
	}

	return filepath.Join(dir, "config"+exts[0]), nil
}

func userConfigBase() (string, error) {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("APPDATA"); base != "" {
			return base, nil
		}
	} else if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base, nil
	}

	home, err := os.UserHomeDir()
	if err != nil && home == "" {
		home = os.Getenv("HOME")
	}

	if home == "" {
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}

		return "", errNoHome
	}

	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming"), nil
	}

	return filepath.Join(home, ".config"), nil
}

func discoverExplicitLayer(opts DiscoverOptions) ([]Layer, error) {
	if opts.ExplicitPath == "-" {
		data, err := stream.ReadStdin(opts.StdinReader, opts.MaxFileSize)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}

		return []Layer{
			{
				Source: SourceStdin,
				Path:   "-",
				Data:   data,
			},
		}, nil
	}

	cleanPath := filepath.Clean(opts.ExplicitPath)

	info, err := os.Stat(cleanPath)
	if err != nil {
		if opts.OptionalPath && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("explicit configuration path %s: %w", opts.ExplicitPath, err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrPathIsDirectory, opts.ExplicitPath)
	}

	return []Layer{
		{
			Source: SourceFile,
			Path:   opts.ExplicitPath,
			Data:   nil,
		},
	}, nil
}

func discoverSystemLayer(appName string, exts []string) string {
	if runtime.GOOS == "windows" {
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}

		appDir := filepath.Join(programData, appName)

		return findConfigFile(appDir, exts)
	}

	dirs := os.Getenv("XDG_CONFIG_DIRS")

	var candidates []string
	if dirs != "" {
		candidates = strings.Split(dirs, ":")
	} else {
		candidates = []string{"/etc/xdg"}
	}

	for _, dir := range candidates {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" {
			continue
		}

		appDir := filepath.Join(trimmed, appName)
		if found := findConfigFile(appDir, exts); found != "" {
			return found
		}
	}

	return ""
}

func discoverUserLayer(appName string, exts []string) string {
	base, err := userConfigBase()
	if err != nil {
		return ""
	}

	return findConfigFile(filepath.Join(base, appName), exts)
}

func findConfigFile(dir string, exts []string) string {
	for _, ext := range exts {
		fullPath := filepath.Join(dir, "config"+ext)
		if isRegularFile(fullPath) {
			return fullPath
		}
	}

	return ""
}

// Opening a FIFO with no writer blocks indefinitely, so only regular files
// are accepted. Symlinks to regular files are followed.
func isRegularFile(path string) bool {
	info, err := os.Stat(filepath.Clean(path))
	if err != nil {
		return false
	}

	return info.Mode().IsRegular()
}
