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
	// XDG_CONFIG_HOME on Unix, %APPDATA% on Windows, and the XDG or Application
	// Support directories on macOS.
	SourceUser = "user"

	// SourceProject marks a layer discovered in the project tier: the working
	// directory, which is [Params.CWD] when set and the process working directory
	// otherwise.
	//
	// A path named by [Params.ExplicitPath] is reported with this source as well.
	SourceProject = "project"

	// SourceStdin marks the layer read from standard input, which
	// [Params.ExplicitPath] selects with "-".
	SourceStdin = "stdin"
)

// ErrPathIsDirectory is returned when an explicit configuration path names a
// directory.
//
// The error carries the path.
var ErrPathIsDirectory = errors.New("explicit configuration path is a directory")

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
type Params struct {
	AppName      string
	ExplicitPath string
	CWD          string
	WithoutFiles bool
	StdinReader  io.Reader
	MaxFileSize  int64
	Extensions   []string
}

// Discover returns the configuration layers to merge, in ascending precedence.
//
// The tiers are searched in the order system, user, project, and each tier
// contributes at most one layer. A tier with no configuration file contributes
// nothing; that is not an error.
//
// An explicit path is honored ahead of every tier and outranks WithoutFiles,
// which disables tier scanning only. The path "-" reads standard input in place
// of a file.
//
// WithoutFiles and an empty AppName both yield no layers when no explicit path
// is set.
//
// It returns [ErrPathIsDirectory] if an explicit path names a directory.
func Discover(p Params) ([]Layer, error) {
	// An explicit path is checked before WithoutFiles: naming one file is a more
	// specific instruction than disabling tier discovery.
	if p.ExplicitPath != "" {
		return discoverExplicitLayer(p)
	}

	if p.WithoutFiles {
		return nil, nil
	}

	if p.AppName == "" {
		return nil, nil
	}

	var layers []Layer

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

	if projPath := discoverProjectLayer(p.CWD, p.AppName, p.Extensions); projPath != "" {
		layers = append(layers, Layer{
			Source: SourceProject,
			Path:   projPath,
			Data:   nil,
		})
	}

	return layers, nil
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
		return nil, fmt.Errorf("explicit configuration path %s: %w", p.ExplicitPath, err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrPathIsDirectory, p.ExplicitPath)
	}

	return []Layer{
		{
			Source: SourceProject,
			Path:   p.ExplicitPath,
			Data:   nil,
		},
	}, nil
}

func discoverProjectLayer(cwd, appName string, exts []string) string {
	dir := cwd
	if dir == "" {
		if cur, err := os.Getwd(); err == nil {
			dir = cur
		}
	}

	if dir == "" {
		return ""
	}

	return discoverProjectTier(dir, appName, exts)
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
	home, err := os.UserHomeDir()
	if err != nil && home == "" {
		home = os.Getenv("HOME")
	}

	switch runtime.GOOS {
	case "windows":
		return discoverWindowsUserTier(appName, home, exts)
	case "darwin":
		return discoverDarwinUserTier(appName, home, exts)
	default:
		return discoverUnixUserTier(appName, home, exts)
	}
}

func discoverWindowsUserTier(appName, home string, exts []string) string {
	appData := os.Getenv("APPDATA")
	if appData == "" && home != "" {
		appData = filepath.Join(home, "AppData", "Roaming")
	}

	if appData != "" {
		appDir := filepath.Join(appData, appName)

		return findConfigFile(appDir, exts)
	}

	return ""
}

func discoverDarwinUserTier(appName, home string, exts []string) string {
	if xdgHome := os.Getenv("XDG_CONFIG_HOME"); xdgHome != "" {
		appDir := filepath.Join(xdgHome, appName)
		if found := findConfigFile(appDir, exts); found != "" {
			return found
		}
	}

	if home != "" {
		appDir := filepath.Join(home, "Library", "Application Support", appName)
		if found := findConfigFile(appDir, exts); found != "" {
			return found
		}
	}

	return ""
}

func discoverUnixUserTier(appName, home string, exts []string) string {
	xdgHome := os.Getenv("XDG_CONFIG_HOME")
	if xdgHome == "" && home != "" {
		xdgHome = filepath.Join(home, ".config")
	}

	if xdgHome != "" {
		appDir := filepath.Join(xdgHome, appName)

		return findConfigFile(appDir, exts)
	}

	return ""
}

func discoverProjectTier(cwd, appName string, exts []string) string {
	for _, ext := range exts {
		fileName := fmt.Sprintf(".%s%s", appName, ext)
		fullPath := filepath.Join(cwd, fileName)

		if isRegularFile(fullPath) {
			return fullPath
		}
	}

	appDir := filepath.Join(cwd, "."+appName)

	return findConfigFile(appDir, exts)
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
