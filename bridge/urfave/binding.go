package strataurfave

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/defaulter"
	"github.com/zigai/strata/internal/plan"
)

const (
	showColumnPadding = 2
	configSetArgCount = 2

	shellCompletionFlag = "--generate-shell-completion"
)

var errSetArgs = errors.New("set requires a key and value")

type missingConfigError struct{ path string }

type boundFlag struct {
	target plan.Leaf
	flag   cli.Flag
}

// Binding connects urfave/cli flags to a configuration struct and records the
// metadata from its most recent successful load.
type Binding[T any] struct {
	cfg        *T
	opts       []strata.Option
	meta       *strata.Metadata
	flags      []boundFlag
	defaults   reflect.Value
	configPath string
	appName    string
	skipped    map[*cli.Command]bool
}

func (e *missingConfigError) Error() string {
	return e.path + " does not exist; create it with config init"
}

func (e *missingConfigError) Unwrap() error { return fs.ErrNotExist }

// Bind prepares key-based flags for cfg. Set the root command's Before to
// [Binding.Before] and include [Binding.ConfigFlag] to enable --config.
// The root command name selects the default system and user config layers. Pass
// [strata.WithFormats] to enable file loading; without it, command execution
// returns [strata.ErrNoFormats].
func Bind[T any](cfg *T, opts ...strata.Option) *Binding[T] {
	if cfg == nil || reflect.TypeFor[T]().Kind() != reflect.Struct {
		panic("strataurfave.Bind requires a pointer to a struct")
	}

	var defaults T
	if err := defaulter.Apply(&defaults, nil); err != nil {
		panic(err)
	}

	return &Binding[T]{cfg: cfg, opts: opts, meta: nil, flags: nil, defaults: reflect.ValueOf(defaults), configPath: "", appName: "", skipped: make(map[*cli.Command]bool)}
}

// Flag returns a typed cli.Flag for key, with its SetDefaults value. Invalid,
// unsupported, or secret keys panic. The first alias is its shorthand name.
func (b *Binding[T]) Flag(key, usage string, aliases ...string) cli.Flag {
	target, err := plan.LeafFor(reflect.TypeFor[T](), key)
	if err != nil {
		panic(err)
	}

	if target.IsSecret {
		panic(fmt.Sprintf("secret key %q cannot be a flag", key))
	}

	if len(aliases) > 0 {
		target.Shorthand = aliases[0]
	}

	target.Usage = usage

	storage := reflect.New(plan.StorageTypeFor(target.Kind, target.Type))
	if err := plan.SeedStorage(storage, b.defaults, &target); err != nil {
		panic(err)
	}

	flag, err := bindFlag(&pendingFlag{target: target, storage: storage})
	if err != nil {
		panic(err)
	}

	b.flags = append(b.flags, boundFlag{target: target, flag: flag})

	return flag
}

// ConfigFlag returns the -c/--config flag used by this binding.
func (b *Binding[T]) ConfigFlag() cli.Flag {
	return &cli.StringFlag{Name: "config", Aliases: []string{"c"}, Usage: "config file to load on top of the user config", Destination: &b.configPath}
}

// Before loads the selected command's configuration before its action. Assign
// it to the root command's Before hook. Commands created by ConfigCommand for
// set, init, and path skip loading so they can repair an invalid file.
func (b *Binding[T]) Before(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	b.appName = cmd.Root().Name

	selected := cmd.Root()
	for _, name := range selected.Args().Slice() {
		next := selected.Command(name)
		if next == nil {
			break
		}

		selected = next
	}

	if b.skipped[selected] || isBuiltIn(selected) || completionRequested(cmd.Root()) {
		return ctx, nil
	}

	return ctx, b.Load(selected)
}

func isBuiltIn(cmd *cli.Command) bool {
	for _, current := range cmd.Lineage() {
		lineage := current.Lineage()

		switch {
		case current.Name == "help" && len(lineage) > 1:
			return true
		case current.Name == "completion" && len(lineage) == 2:
			return true
		}
	}

	return false
}

// urfave/cli removes its completion flag before Before runs and keeps
// that state private, so process arguments are the only public signal.
func completionRequested(root *cli.Command) bool {
	return root.EnableShellCompletion && len(os.Args) > 1 && os.Args[len(os.Args)-1] == shellCompletionFlag
}

// Load merges defaults, files, environment, and changed bound flags into cfg.
func (b *Binding[T]) Load(cmd *cli.Command) error {
	opts := append([]strata.Option{strata.WithAppName(cmd.Root().Name)}, b.opts...)
	if b.configPath != "" {
		opts = append(opts, strata.WithPath(b.configPath))
	}

	opts = append(opts, strata.WithContribution(func(target any, meta *strata.Metadata) error {
		root, err := plan.StructTarget(target)
		if err != nil {
			return err
		}

		for _, bound := range b.flags {
			if !sameFlag(activeFlag(cmd, bound.target.FlagName), bound.flag) {
				continue
			}

			t := bound.target
			if err := syncTarget(cmd, root, &t, meta); err != nil {
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

func activeFlag(cmd *cli.Command, name string) cli.Flag {
	for _, current := range cmd.Lineage() {
		for _, flag := range current.Flags {
			if slices.Contains(flag.Names(), name) {
				return flag
			}
		}

		for _, group := range current.MutuallyExclusiveFlags {
			for _, flags := range group.Flags {
				for _, flag := range flags {
					if slices.Contains(flag.Names(), name) {
						return flag
					}
				}
			}
		}
	}

	return nil
}

func sameFlag(left, right cli.Flag) bool {
	a := reflect.ValueOf(left)
	b := reflect.ValueOf(right)

	return a.IsValid() && b.IsValid() && a.Kind() == reflect.Pointer && b.Kind() == reflect.Pointer && a.Pointer() == b.Pointer()
}

// Metadata returns provenance from the latest successful load, or nil before
// the first load.
func (b *Binding[T]) Metadata() *strata.Metadata { return b.meta }

// EditPath returns the file used for edits: --config or WithPath when set,
// otherwise the user-layer file selected by the loading options.
func (b *Binding[T]) EditPath() (string, error) {
	opts := append([]strata.Option{strata.WithAppName(b.appName)}, b.opts...)
	if b.configPath != "" {
		opts = append(opts, strata.WithPath(b.configPath))
	}

	path, err := strata.EditPath(opts...)
	if err != nil {
		return "", fmt.Errorf("find user configuration file: %w", err)
	}

	return path, nil
}

// Set checks key and value against T, then edits EditPath's file. A missing file
// reports that config init should be run first. It does not run ValidateWith.
func (b *Binding[T]) Set(key, value string) (string, error) {
	path, err := b.EditPath()
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "", &missingConfigError{path: path}
	} else if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}

	if err := strata.Set[T](path, key, value); err != nil {
		return "", err
	}

	return path, nil
}

// Init writes a new user config file at EditPath, creating its parent directory.
// Secrets are omitted from the template.
func (b *Binding[T]) Init() (string, error) {
	path, err := b.EditPath()
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
func (b *Binding[T]) ConfigCommand() *cli.Command {
	set := b.configSetCommand()
	init := b.configInitCommand()
	path := b.configPathCommand()
	b.skipped[set] = true
	b.skipped[init] = true
	b.skipped[path] = true

	return &cli.Command{Name: "config", Usage: "Show and edit the configuration", Commands: []*cli.Command{b.configShowCommand(), set, init, path}}
}

func (b *Binding[T]) configShowCommand() *cli.Command {
	return &cli.Command{Name: "show", Usage: "Show every setting and where its value came from", Action: func(_ context.Context, cmd *cli.Command) error {
		for _, unknown := range b.meta.UnknownKeys() {
			if _, err := fmt.Fprintf(cmd.ErrWriter, "warning: %s: %s\n", unknown.Path, unknown.String()); err != nil {
				return fmt.Errorf("write warning: %w", err)
			}
		}

		w := tabwriter.NewWriter(cmd.Writer, 0, 0, showColumnPadding, ' ', 0)
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

func (b *Binding[T]) configSetCommand() *cli.Command {
	return &cli.Command{Name: "set", Usage: "Change one setting in the config file", ArgsUsage: "<key> <value>", Action: func(_ context.Context, cmd *cli.Command) error {
		if cmd.Args().Len() != configSetArgCount {
			return errSetArgs
		}

		path, err := b.Set(cmd.Args().Get(0), cmd.Args().Get(1))
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(cmd.Writer, "%s: set %s\n", path, cmd.Args().Get(0)); err != nil {
			return fmt.Errorf("write set result: %w", err)
		}

		return nil
	}}
}

func (b *Binding[T]) configInitCommand() *cli.Command {
	return &cli.Command{Name: "init", Usage: "Write a config file with every default filled in", Action: func(_ context.Context, cmd *cli.Command) error {
		path, err := b.Init()
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintln(cmd.Writer, "wrote", path); err != nil {
			return fmt.Errorf("write init result: %w", err)
		}

		return nil
	}}
}

func (b *Binding[T]) configPathCommand() *cli.Command {
	return &cli.Command{Name: "path", Usage: "Show the config file path", Action: func(_ context.Context, cmd *cli.Command) error {
		resolved, err := b.EditPath()
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintln(cmd.Writer, resolved); err != nil {
			return fmt.Errorf("write configuration path: %w", err)
		}

		return nil
	}}
}
