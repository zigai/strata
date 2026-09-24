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
	// SourceSystem marks a layer discovered in the system tier: /etc/xdg or the
	// XDG_CONFIG_DIRS entries, and %ProgramData% on Windows.
	SourceSystem = "system"

	// SourceUser marks a layer discovered in the user tier: ~/.config or
	// XDG_CONFIG_HOME on Unix and macOS, and %APPDATA% on Windows.
	SourceUser = "user"

	// SourceFile marks the layer read from the path named by
	// [Params.ExplicitPath].
	SourceFile = "file"

	// SourceStdin marks the layer read from standard input, which
	// [Params.ExplicitPath] selects with "-".
	SourceStdin = "stdin"
)

// ErrPathIsDirectory is returned when an explicit configuration path names a
// directory.
//
// The error carries the path.
var ErrPathIsDirectory = errors.New("explicit configuration path is a directory")

var (
	errNoConfigFormat = errors.New("no configuration format is enabled")
	errNoHome         = errors.New("home directory is not available")
)

// Layer is one configuration source discovered in the cascade order.
//
// Source is one of the Source* constants. Data holds the bytes of a stdin layer
// and is nil for a file layer, whose contents the caller reads from Path.
type Layer struct {
	Source string
	Path   string
	Data   []byte
}

// Params specifies the input to [Discover].
//
// Extensions are tried in the order given. MaxFileSize bounds the bytes read for
// a stdin layer, and StdinReader overrides the process standard input.
//
// OptionalPath makes a missing ExplicitPath contribute nothing instead of
// failing the discovery.
type Params struct {
	AppName      string
	ExplicitPath string
	OptionalPath bool
	WithoutFiles bool
	StdinReader  io.Reader
	MaxFileSize  int64
	Extensions   []string
}

// Discover returns the configuration layers to merge, in ascending precedence.
//
// AppName selects up to one system and one user file. Extensions are tried in
// the order in Params. A missing tier file contributes nothing. Empty AppName
// or WithoutFiles skips both tiers.
//
// ExplicitPath applies above them even with WithoutFiles. "-" reads stdin;
// missing paths fail unless OptionalPath is set.
//
// It returns [ErrPathIsDirectory] if an explicit path names a directory.
func Discover(p Params) ([]Layer, error) {
	if p.WithoutFiles {
		if p.ExplicitPath != "" {
			return discoverExplicitLayer(p)
		}

		return nil, nil
	}

	var layers []Layer

	if p.AppName != "" {
		if sysPath := discoverSystemTier(p.AppName, p.Extensions); sysPath != "" {
			layers = append(layers, Layer{
				Source: SourceSystem,
				Path:   sysPath,
				Data:   nil,
			})
		}

		if userPath := discoverUserTier(p.AppName, p.Extensions); userPath != "" {
			layers = append(layers, Layer{
				Source: SourceUser,
				Path:   userPath,
				Data:   nil,
			})
		}
	}

	if p.ExplicitPath != "" {
		explicitLayers, err := discoverExplicitLayer(p)
		if err != nil {
			return nil, err
		}

		layers = append(layers, explicitLayers...)
	}

	return layers, nil
}

func UserConfigFile(appName string) (string, error) {
	return UserConfigFileForExtensions(appName, []string{".toml", ".yaml", ".yml", ".json"})
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

func discoverExplicitLayer(p Params) ([]Layer, error) {
	if p.ExplicitPath == "-" {
		data, err := stream.ReadStdin(p.StdinReader, p.MaxFileSize)
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

	cleanPath := filepath.Clean(p.ExplicitPath)

	info, err := os.Stat(cleanPath)
	if err != nil {
		if p.OptionalPath && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("explicit configuration path %s: %w", p.ExplicitPath, err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrPathIsDirectory, p.ExplicitPath)
	}

	return []Layer{
		{
			Source: SourceFile,
			Path:   p.ExplicitPath,
			Data:   nil,
		},
	}, nil
}

func discoverSystemTier(appName string, exts []string) string {
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

func discoverUserTier(appName string, exts []string) string {
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

// isRegularFile reports whether path names a regular file.
//
// Directories, FIFOs, sockets, and device nodes are rejected. Opening a FIFO
// with no writer blocks indefinitely, and the rest are not configuration
// documents.
//
// Symlinks are followed: a link to a regular file is accepted.
func isRegularFile(path string) bool {
	info, err := os.Stat(filepath.Clean(path))
	if err != nil {
		return false
	}

	return info.Mode().IsRegular()
}
