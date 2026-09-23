<a href="https://github.com/zigai/strata"><img src="assets/logo.svg" align="right" width="130" alt="Strata gopher holding a layered rock column"></a>

# strata

**Configuration for Go that remembers where every value came from.**

Put your settings in a struct with some defaults. strata fills it in from config files, environment variables, and CLI flags. If a value looks wrong, you can ask where it came from.

[![Tests](https://img.shields.io/github/actions/workflow/status/zigai/strata/test.yml?branch=master&label=Tests)](https://github.com/zigai/strata/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/zigai/strata?color=blue)](https://github.com/zigai/strata/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/zigai/strata.svg)](https://pkg.go.dev/github.com/zigai/strata)
[![License](https://img.shields.io/github/license/zigai/strata)](LICENSE)

<br clear="right">

- **One struct is the whole config.** No string keys, no separate schema.
- **Layers stack up.** Files, environment variables, and flags each override the one below.
- **Every value has a source.** Ask strata which file, and which line, set it.
- **Edits keep the user's formatting.** Change a key without losing their comments.

## Install

```sh
go get github.com/zigai/strata
```

Requires Go 1.27 or newer.

## Quick start

```go
package main

import (
	"fmt"
	"log"

	"github.com/zigai/strata"
)

type Config struct {
	Host string
	Port int
}

func (c *Config) SetDefaults() {
	c.Host = "127.0.0.1"
	c.Port = 8080
}

func main() {
	cfg, err := strata.Load[Config](strata.WithPath("config.toml"))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("listening on %s:%d\n", cfg.Host, cfg.Port)
}
```

Put `port = 9000` in `config.toml` and run it:

```text
listening on 127.0.0.1:9000
```

The file changed `port`, and `host` kept its default. No struct tags needed: `Port` reads the `port` key automatically.

The file has to exist. If it's optional, use `WithOptionalPath` instead.

## How the layers stack

Each layer overrides the ones below it, one key at a time:

```text
  CLI flags        --port 9100          ← wins
  Environment      MYAPP_PORT=9050
  Config file      ./config.toml
  User file        ~/.config/myapp/
  System file      /etc/xdg/myapp/
  Defaults         SetDefaults()        ← fallback
```

Only defaults are on by default. You turn on each of the others with one option.

### Environment variables

```go
cfg, err := strata.Load[Config](
	strata.WithPath("config.toml"),
	strata.WithEnvPrefix("MYAPP_"),
)
```

Now `MYAPP_PORT=9050` overrides the file. Nested keys use underscores, so `database.port` becomes `MYAPP_DATABASE_PORT`.

To list every variable your program reads, for a `--help` page or your docs, call `strata.EnvVars[Config]("MYAPP_")`.

### System and user config files

```go
cfg, err := strata.Load[Config](strata.WithAppName("myapp"))
```

strata then checks `/etc/xdg/myapp/` and `~/.config/myapp/` for a `config.toml`, `.yaml`, `.yml`, or `.json`. On Windows it checks `%ProgramData%` and `%APPDATA%`.

### CLI flags

Tag the fields you want as flags:

```go
type Config struct {
	Host string `flag:"host"`
	Port int    `flag:"port,p"` // -p, --port
}
```

Then, with [Cobra](https://github.com/spf13/cobra):

```go
cfg.SetDefaults() // so --help shows them
stratacobra.RegisterFlags(cmd, &cfg)

// later, in PersistentPreRunE:
cfg, err := strata.Load[Config](
	strata.WithAppName("myapp"),
	stratacobra.WithFlags(cmd),
)
```

Only the flags the user actually typed override anything. [`urfave/cli`](https://github.com/urfave/cli) works the same way through `strataurfave`. Both bridges are separate modules:

```sh
go get github.com/zigai/strata/bridge/cobra
go get github.com/zigai/strata/bridge/urfave
```

For a complete CLI, see [`examples/cli`](examples/cli).

## Where did this value come from?

Use `LoadWithMetadata` to get a `*Metadata` along with your config, then ask it about any key:

```go
cfg, meta, err := strata.LoadWithMetadata[Config](strata.WithAppName("myapp"))

origin, _ := meta.Where("port")
fmt.Println(origin.Source, origin.Path)
// user /home/you/.config/myapp/config.toml
```

`Source` is one of `default`, `system`, `user`, `file`, `stdin`, `env`, or `flag`. For YAML files you also get `origin.Line`. `meta.Origins()` lists every key at once, which is handy for a `config show` command.

## Catching typos

A key your struct doesn't have, like `prot = 9000`, does nothing. strata notices and keeps track of it:

```go
for _, key := range meta.UnknownKeys() {
	log.Printf("%s: unknown key %q", key.Path, key.Key)
}
```

Each one also has a `Suggestion`: the closest key your struct does have, or empty if nothing is close.

To turn these into errors instead, pass `strata.WithStrict()`:

```text
config error: unknown key "prot" (did you mean "port"?)
  --> set by /home/you/project/config.toml
```

## Checking values

Add a `Validate` method and strata runs it after all the layers, flags included, have been merged:

```go
func (c *Config) Validate() error {
	if c.Port > 9000 {
		return errors.New("port is too high")
	}
	return nil
}
```

If you'd like the error to point at the file that caused the problem, write `ValidateWith` instead:

```go
func (c *Config) ValidateWith(meta *strata.Metadata) error {
	if c.Port > 9000 {
		return meta.NewConfigError("port", errors.New("above the privileged ceiling"))
	}
	return nil
}
```

```text
config error: above the privileged ceiling for port: "9090"
  --> set by /home/you/project/config.toml
```

## Editing config files

`Set` changes one key and leaves the rest of the file alone:

```go
err := strata.Set("config.toml", "database.port", 5433)
```

TOML files keep everything: comments, blank lines, and key order. YAML files keep their comments but lose blank lines. JSON files get their keys sorted.

For files only your program touches, like saved state, `Save` writes the whole struct at once:

```go
err := strata.Save("state.json", cfg)
```

Both write atomically, so a crash never leaves a half-written file. They also use the same key names strata reads, so `Set` works on a file that `Save` or `Init` wrote.

## Starter files and schemas

`Init` writes a config file full of your defaults. It never overwrites an existing file unless you pass `WithOverwrite(true)`.

```go
err := strata.Init[Config]("config.toml")
```

`Schema` generates a JSON Schema from your struct. Doc comments on fields become descriptions, so editors can autocomplete and explain each setting:

```go
schema, err := strata.Schema[Config]()
```

Pass `strata.WithSchemaURL(url)` to `Init` to link the two.

## Days and weeks

Go's `time.Duration` stops at hours. `strata.Duration` also understands `7d`, `2w`, and `1w2d`:

```go
type Config struct {
	Timeout strata.Duration // "2w" works
}
```

## More details

<details>
<summary><b>All loading options</b></summary>

<br>

| Option | What it does |
| --- | --- |
| `WithPath(path)` | Load this file on top of the system and user files. Use `-` for standard input. |
| `WithOptionalPath(path)` | Same as `WithPath`, but a missing file is skipped. |
| `WithAppName(name)` | Look for system and user config files under this name. |
| `WithEnvPrefix(prefix)` | Read environment variables that start with this prefix. |
| `WithStrict()` | Fail when a config file sets a key your struct doesn't have. |
| `WithDefaults(value)` | Use this value as the defaults, in place of what `SetDefaults` set. |
| `WithoutFiles()` | Skip system and user files. A `WithPath` file still loads. |
| `WithFormats("toml", "yaml")` | Only look for these formats, in this order. |
| `WithMaxFileSize(bytes)` | Reject bigger files with `ErrFileTooLarge`. The default is 1 MiB. |

</details>

<details>
<summary><b>Key names and secrets</b></summary>

<br>

A field's key comes from its `strata`, `toml`, `yaml`, or `json` tag, whichever it finds first. Without one, the field name is converted to snake case. Nested structs use dots: `database.port`.

An `env` tag gives a field an exact variable name. It's read even without `WithEnvPrefix`. Fields without one are only read from the environment once you set a prefix, so a stray `PORT` or `HOST` in the environment never changes your config.

Tag a field `secret` to keep its value out of provenance output (it shows `[REDACTED]`) and out of `--help`:

```go
type Config struct {
	DatabaseURL string `strata:"db_url"`
	APIKey      string `env:"API_KEY,secret"`
}
```

</details>

<details>
<summary><b>Flag tags</b></summary>

<br>

| Tag | Flag |
| --- | --- |
| `flag:"port"` | `--port` |
| `flag:"port,p"` | `-p, --port` |
| `flag:",p"` | Name from the field, plus `-p` |
| `flag:""` | Name from the field |

Fields without a `flag` tag never become flags. Flags on an embedded struct are added as if they were declared on the parent. A tagged nested struct prefixes its flags, so `flag:"db"` on a `Database` field gives `--db-host`.

</details>

<details>
<summary><b>Other file formats</b></summary>

<br>

```go
// Treat .conf files as TOML.
strata.WithFormatAlias(".conf", "toml")

// Use any unmarshal function.
strata.WithDecoder(".json5", json5.Unmarshal)

// Decode straight into your type.
strata.WithDecoderFunc(".env", func(data []byte, target *Config) error {
	// ...
	return nil
})
```

To write the format as well, implement `Codec` and register it with `WithCodec`.

</details>

<details>
<summary><b>Errors</b></summary>

<br>

A missing system or user file is skipped, and so is a missing `WithOptionalPath` file. Everything else is an error: a missing `WithPath` file, a file that can't be read or parsed, and a failed validation. When `Load` fails, it returns the zero value.

Every error can be matched with `errors.Is` against a sentinel in the `strata` package:

```go
if errors.Is(err, strata.ErrMalformed) {
	// The file exists but couldn't be parsed.
}
```

Validation failures are always a `*strata.ConfigError`.

</details>

## License

[MIT](LICENSE)
