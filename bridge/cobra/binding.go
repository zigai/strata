package stratacobra

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/defaulter"
	"github.com/zigai/strata/internal/plan"
)

const (
	showColumnPadding  = 2
	configSetArgCount  = 2
	skipLoadAnnotation = "github.com/zigai/strata/skip-load"
)

type boundFlag struct {
	target plan.Target
	flag   *pflag.Flag
}

type missingConfigError struct {
	path string
	init string
}

// Binding connects Cobra flags to a configuration struct and keeps the metadata
// from its most recent successful load.
type Binding[T any] struct {
	root       *cobra.Command
	cfg        *T
	opts       []strata.Option
	configPath string
	meta       *strata.Metadata
	targets    []boundFlag
	defaults   reflect.Value
}

func (e *missingConfigError) Error() string {
	return fmt.Sprintf("%s does not exist; create it with `%s config init`", e.path, e.init)
}

func (e *missingConfigError) Unwrap() error { return fs.ErrNotExist }

// Bind installs configuration loading on root. Cobra runs only the nearest
// PersistentPreRunE hook: a child that defines its own must call [Binding.Load].
// Hooks already on root run before loading. Set root hooks before Bind:
// replacing PersistentPreRunE afterward removes this load step, and a later
// PersistentPreRun is ignored while PersistentPreRunE is set.
//
// Bind adds persistent -c/--config flags. It uses root's name for system and
// user file discovery unless a later WithAppName option overrides it. It panics
// if root or cfg is nil, or cfg does not point to a struct. Pass
// [strata.WithFormats] to enable file loading; without it, command execution
// returns [strata.ErrNoFormats].
func Bind[T any](root *cobra.Command, cfg *T, opts ...strata.Option) *Binding[T] {
	if root == nil || cfg == nil || reflect.TypeFor[T]().Kind() != reflect.Struct {
		panic("stratacobra.Bind requires a command and a pointer to a struct")
	}

	var defaults T
	if err := defaulter.Apply(&defaults, nil); err != nil {
		panic(err)
	}

	resolvedOpts := append([]strata.Option{strata.WithAppName(root.Name())}, opts...)
	b := &Binding[T]{root: root, cfg: cfg, opts: resolvedOpts, configPath: "", meta: nil, targets: nil, defaults: reflect.ValueOf(defaults)}
	root.PersistentFlags().StringVarP(&b.configPath, "config", "c", "", "config file to load on top of the user config")
	previous := root.PersistentPreRun
	previousE := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if previous != nil {
			previous(cmd, args)
		}

		if previousE != nil {
			if err := previousE(cmd, args); err != nil {
				return err
			}
		}

		if skipCommand(cmd) {
			return nil
		}

		return b.Load(cmd)
	}

	return b
}

// SkipLoad marks commands whose execution must bypass configuration loading.
// A marked parent also skips loading for its descendants.
func SkipLoad(cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		if cmd != nil {
			if cmd.Annotations == nil {
				cmd.Annotations = make(map[string]string)
			}

			cmd.Annotations[skipLoadAnnotation] = "true"
		}
	}
}

func skipCommand(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Annotations[skipLoadAnnotation] == "true" || isBuiltIn(current) {
			return true
		}
	}

	return false
}

// isBuiltIn reports whether cmd is one of the commands Cobra adds to the root:
// help, completion, and the hidden shell-completion requests. Cobra adds them
// only at the root, and a root child a program defines under the same name
// replaces the built-in and serves the same purpose, so a command deeper in the
// tree that happens to share a name still loads configuration.
func isBuiltIn(cmd *cobra.Command) bool {
	parent := cmd.Parent()
	if parent == nil || parent.HasParent() {
		return false
	}

	switch cmd.Name() {
	case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	default:
		return false
	}
}

// Flag adds a flag for key to fs, using the field's type and SetDefaults value.
// A dotted key uses hyphens in its flag name. Invalid, unsupported, or secret
// keys panic, as do duplicate flag names in the same flag set.
func (b *Binding[T]) Flag(fs *pflag.FlagSet, key, usage string) {
	b.FlagP(fs, key, "", usage)
}

// FlagP is Flag with a shorthand flag name.
func (b *Binding[T]) FlagP(fs *pflag.FlagSet, key, shorthand, usage string) {
	if fs == nil {
		panic("stratacobra.Flag requires a flag set")
	}

	target, err := plan.Lookup(reflect.TypeFor[T](), key)
	if err != nil {
		panic(err)
	}

	if target.Secret {
		panic(fmt.Sprintf("secret key %q cannot be a flag", key))
	}

	target.Usage = usage
	target.Shorthand = shorthand

	storage := reflect.New(plan.StorageTypeFor(target.Kind, target.LeafType))
	if err := plan.SeedStorage(storage, b.defaults, &target); err != nil {
		panic(err)
	}

	if err := bindFlag(fs, &pendingFlag{target: target, storage: storage}); err != nil {
		panic(err)
	}

	b.targets = append(b.targets, boundFlag{target: target, flag: fs.Lookup(target.Name)})
}

// Load merges defaults, files, environment, and changed flags into the bound
// struct. Call it from a child PersistentPreRunE that overrides root's hook.
func (b *Binding[T]) Load(cmd *cobra.Command) error {
	cmd.SilenceUsage = true

	opts := append([]strata.Option(nil), b.opts...)
	if b.configPath != "" {
		opts = append(opts, strata.WithPath(b.configPath))
	}

	opts = append(opts, strata.WithContribution(func(target any, meta *strata.Metadata) error {
		root, err := plan.StructTarget(target)
		if err != nil {
			return err
		}

		for _, bound := range b.targets {
			if err := syncTarget(cmd, root, &bound, meta); err != nil {
				return err
			}
		}

		return nil
	}))

	var loaded T

	meta, err := strata.LoadInto(&loaded, opts...)
	if err != nil {
		//nolint:wrapcheck // LoadInto already returns a contextual configuration error.
		return err
	}

	*b.cfg = loaded
	b.meta = meta

	return nil
}

// Metadata returns provenance from the latest successful load, or nil before
// the first load.
func (b *Binding[T]) Metadata() *strata.Metadata { return b.meta }

// Path returns the file used for edits: --config or WithPath when set,
// otherwise the user-tier file selected by the loading options.
func (b *Binding[T]) Path() (string, error) {
	opts := append([]strata.Option(nil), b.opts...)
	if b.configPath != "" {
		opts = append(opts, strata.WithPath(b.configPath))
	}

	path, err := strata.ConfigEditPath(opts...)
	if err != nil {
		return "", fmt.Errorf("find user configuration file: %w", err)
	}

	return path, nil
}

// Set checks key and value against T, then edits Path's file. A missing file
// reports that config init should be run first. It does not run ValidateWith.
func (b *Binding[T]) Set(key, value string) (string, error) {
	path, err := b.Path()
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "", &missingConfigError{path: path, init: b.root.Name()}
	} else if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}

	if err := strata.Set[T](path, key, value); err != nil {
		return "", err
	}

	return path, nil
}

// Init writes a new user config file at Path, creating its parent directory.
// Secrets are omitted from the template.
func (b *Binding[T]) Init() (string, error) {
	path, err := b.Path()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("create configuration directory: %w", err)
	}

	if err := strata.Init[T](path); err != nil {
		return "", err
	}

	return path, nil
}

// ConfigCommand builds config show, set, init, and path from this binding.
// Set, init, and path run even when an existing configuration file is invalid.
func (b *Binding[T]) ConfigCommand() *cobra.Command {
	config := &cobra.Command{Use: "config", Short: "Show and edit the configuration"}
	set := b.configSetCommand()
	init := b.configInitCommand()
	path := b.configPathCommand()
	SkipLoad(set, init, path)
	config.AddCommand(b.configShowCommand(), set, init, path)

	return config
}

func (b *Binding[T]) configShowCommand() *cobra.Command {
	return &cobra.Command{Use: "show", Short: "Show every setting and where its value came from", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		for _, unknown := range b.meta.UnknownKeys() {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %s\n", unknown.Path, unknown.String()); err != nil {
				return fmt.Errorf("write warning: %w", err)
			}
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, showColumnPadding, ' ', 0)
		if _, err := fmt.Fprintln(w, "KEY\tVALUE\tSOURCE"); err != nil {
			return fmt.Errorf("write heading: %w", err)
		}

		for _, origin := range b.meta.Origins() {
			value := origin.RawValue
			if plan.IsUnsetSecret(reflect.ValueOf(*b.cfg), origin.Key) {
				value = "(unset)"
			}

			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", origin.Key, value, origin.Location()); err != nil {
				return fmt.Errorf("write setting: %w", err)
			}
		}

		if err := w.Flush(); err != nil {
			return fmt.Errorf("flush settings: %w", err)
		}

		return nil
	}}
}

func (b *Binding[T]) configSetCommand() *cobra.Command {
	return &cobra.Command{Use: "set <key> <value>", Short: "Change one setting in the config file", Args: cobra.ExactArgs(configSetArgCount), RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		path, err := b.Set(args[0], args[1])
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: set %s\n", path, args[0])
		if err != nil {
			return fmt.Errorf("write set result: %w", err)
		}

		return nil
	}}
}

func (b *Binding[T]) configInitCommand() *cobra.Command {
	return &cobra.Command{Use: "init", Short: "Write a config file with every default filled in", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cmd.SilenceUsage = true

		path, err := b.Init()
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
		if err != nil {
			return fmt.Errorf("write init result: %w", err)
		}

		return nil
	}}
}

func (b *Binding[T]) configPathCommand() *cobra.Command {
	return &cobra.Command{Use: "path", Short: "Show the config file path", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cmd.SilenceUsage = true

		resolved, err := b.Path()
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintln(cmd.OutOrStdout(), resolved); err != nil {
			return fmt.Errorf("write configuration path: %w", err)
		}

		return nil
	}}
}
