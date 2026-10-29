package strata_test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

type secretConfig struct {
	Token string `strata:"token,secret" env:"SECRET_TOKEN"`
}

func (s *secretConfig) SetDefaults() {
	s.Token = "default-insecure-token"
}

type secretFileConfig struct {
	Token string `strata:"token,secret" json:"token"`
}

func (s *secretFileConfig) SetDefaults() {
	s.Token = "default-secret"
}

type secretEncodeValues []string

func (values secretEncodeValues) MarshalJSON() ([]byte, error) {
	return nil, errors.New("invalid " + values[0])
}

func TestRejectedSecretValuesDoNotLeak(t *testing.T) {
	t.Parallel()

	type config struct {
		Credential time.Duration `strata:"credential,secret"`
	}

	for _, test := range []struct {
		name     string
		original string
		reject   func(string) error
	}{
		{
			name:     "load",
			original: `{"credential":"token_"}`,
			reject: func(path string) error {
				_, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("json"))
				return err
			},
		},
		{
			name:     "set",
			original: `{"credential":"1s"}`,
			reject: func(path string) error {
				return strata.Set[config](path, "credential", "token_")
			},
		},
		{
			name:     "set encoding",
			original: `{"credential":[]}`,
			reject: func(path string) error {
				type encodeConfig struct {
					Credential secretEncodeValues `strata:"credential,secret"`
				}

				return strata.Set[encodeConfig](path, "credential", secretEncodeValues{"token_"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.original), 0o600); err != nil {
				t.Fatal(err)
			}

			err := test.reject(path)
			if err == nil {
				t.Fatal("invalid secret was accepted")
			}

			if strings.Contains(err.Error(), "token_") {
				t.Errorf("rejection leaked secret: %v", err)
			}

			if test.name == "load" && !errors.Is(err, strata.ErrMalformed) {
				t.Errorf("Load error = %v, want ErrMalformed", err)
			}

			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}

			if string(data) != test.original {
				t.Errorf("rejected operation changed file: %s", data)
			}
		})
	}
}

func TestSecretRedactsWhenPrinted(t *testing.T) {
	secret := strata.Secret("private-value")
	if secret.String() != "[REDACTED]" || secret.GoString() != "[REDACTED]" || secret.LogValue().String() != "[REDACTED]" || secret.Value() != "private-value" {
		t.Fatal("secret methods")
	}
}

func TestSecretAndDurationRoundTripEveryFormat(t *testing.T) {
	secret := strata.Secret("private-value")

	for ext, data := range map[string]string{
		".toml": "timeout = '45s'\nsecret = 'private-value'\n",
		".json": `{"timeout":"45s","secret":"private-value"}`,
		".yaml": "timeout: 45s\nsecret: private-value\n",
	} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config"+ext)
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, meta, err := strata.LoadWithMetadata[primitiveConfig](strata.WithPath(path), strata.WithFormats(ext))
			if err != nil {
				t.Fatal(err)
			}

			if cfg.Timeout != 45*time.Second || cfg.Secret.Value() != "private-value" {
				t.Fatalf("config = %+v", cfg)
			}

			if origin, _ := meta.Where("secret"); origin.RawValue != "[REDACTED]" {
				t.Fatalf("origin = %+v", origin)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "saved.json")
	if err := strata.Save(path, primitiveConfig{Secret: secret, Timeout: 45 * time.Second}); err != nil {
		t.Fatal(err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(saved), "private-value") || !strings.Contains(string(saved), "45s") {
		t.Fatalf("saved file = %s", saved)
	}
}

type secretNode struct {
	Token strata.Secret `strata:"token"`
	Next  *secretNode   `strata:"next"`
}

func TestSecretCollectionsAndRecursiveOriginsAreRedacted(t *testing.T) {
	t.Parallel()

	type config struct {
		Credentials map[string]string        `strata:"credentials,secret"`
		Typed       map[string]strata.Secret `strata:"typed"`
		Tokens      []strata.Secret          `strata:"tokens"`
		Nodes       []secretNode             `strata:"nodes"`
		Node        *secretNode              `strata:"node"`
	}

	for format, body := range map[string]string{
		"json": `{"credentials":{"service":"private-value"},"typed":{"service":"private-value"},"tokens":["private-value"],"nodes":[{"token":"private-value"}],"node":{"next":{"token":"private-value"}}}`,
		"toml": "tokens = ['private-value']\nnodes = [{token = 'private-value'}]\n[credentials]\nservice = 'private-value'\n[typed]\nservice = 'private-value'\n[node.next]\ntoken = 'private-value'\n",
		"yaml": "credentials:\n  service: private-value\ntyped:\n  service: private-value\ntokens: [private-value]\nnodes:\n  - token: private-value\nnode:\n  next:\n    token: private-value\n",
	} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			cfg, meta, err := strata.LoadWithMetadata[config](strata.WithPath("-"), strata.WithStdin(strings.NewReader(body)), strata.WithFormats(format))
			if err != nil {
				t.Fatal(err)
			}

			if cfg.Credentials["service"] != "private-value" || cfg.Typed["service"].Value() != "private-value" || len(cfg.Tokens) != 1 || cfg.Tokens[0].Value() != "private-value" || len(cfg.Nodes) != 1 || cfg.Nodes[0].Token.Value() != "private-value" || cfg.Node == nil || cfg.Node.Next == nil || cfg.Node.Next.Token.Value() != "private-value" {
				t.Fatalf("config did not load secrets: %+v", cfg)
			}

			for _, key := range []string{"credentials.service", "typed.service", "tokens", "node.next.token"} {
				origin, found := meta.Where(key)
				if !found || origin.RawValue != "[REDACTED]" {
					t.Errorf("Where(%s) = %+v, %t", key, origin, found)
				}

				if message := meta.NewConfigError(key, errors.New("invalid credential")).Error(); strings.Contains(message, "private-value") {
					t.Errorf("diagnostic leaked secret: %s", message)
				}
			}

			for _, origin := range meta.Origins() {
				if strings.Contains(origin.RawValue, "private-value") {
					t.Errorf("origin leaked secret: %+v", origin)
				}
			}
		})
	}
}

func TestDefaultMetadataRedactsSecretsHeldThroughInterfaces(t *testing.T) {
	t.Parallel()

	type config struct{ Value any }

	type credentials struct {
		Token string `strata:"token,secret"`
	}

	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "exported field", value: credentials{Token: "private-value"}},
		{name: "unexported embedding", value: struct{ credentials }{credentials{Token: "private-value"}}},
		{name: "unexported pointer embedding", value: struct{ *credentials }{&credentials{Token: "private-value"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			defaults := config{Value: test.value}

			got, meta, err := strata.LoadWithMetadata[config](strata.WithDefaults(defaults), strata.WithoutFileDiscovery())
			if err != nil {
				t.Fatal(err)
			}

			if got != defaults {
				t.Fatalf("loaded defaults = %+v, want %+v", got, defaults)
			}

			origin, found := meta.Where("value")

			want := strata.Origin{Key: "value", Source: strata.SourceDefault, Path: "", Line: 0, RawValue: "[REDACTED]"}
			if !found || origin != want {
				t.Fatalf("default origin = %+v, found = %t; want %+v", origin, found, want)
			}
		})
	}
}

func TestMixedSecretCollectionsKeepPublicOrigins(t *testing.T) {
	t.Parallel()

	type item struct {
		Token strata.Secret
		Port  int
	}

	type config struct {
		Services map[string]item
		Nodes    []item
	}

	for _, test := range []struct{ format, body string }{
		{"json", `{"services":{"primary":{"token":"private-value","port":9001}},"nodes":[{"token":"private-value","port":9002}]}`},
		{"yaml", "services:\n  primary: {token: private-value, port: 9001}\nnodes: [{token: private-value, port: 9002}]\n"},
		{"toml", "nodes = [{token = 'private-value', port = 9002}]\n[services.primary]\ntoken = 'private-value'\nport = 9001\n"},
	} {
		t.Run(test.format, func(t *testing.T) {
			t.Parallel()

			_, meta, err := strata.LoadWithMetadata[config](strata.WithPath("-"), strata.WithFormats(test.format), strata.WithStdin(strings.NewReader(test.body)))
			if err != nil {
				t.Fatal(err)
			}

			for key, want := range map[string]string{
				"services.primary.port":  "9001",
				"nodes.0.port":           "9002",
				"services.primary.token": "[REDACTED]",
				"nodes.0.token":          "[REDACTED]",
				"nodes":                  "[REDACTED]",
			} {
				if origin, found := meta.Where(key); !found || origin.RawValue != want {
					t.Errorf("Where(%s) = %+v, %t; want %q", key, origin, found, want)
				}
			}
		})
	}
}

func TestRecordSecretRedactsExistingAndDescendantOrigins(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()
	meta.Record(strata.Origin{Key: "credentials", Source: strata.SourceDefault, RawValue: "private-value"})
	meta.Record(strata.Origin{Key: "credentials.service", Source: strata.SourceEnv, RawValue: "private-value"})
	meta.RecordSecret("credentials")
	meta.Record(strata.Origin{Key: "credentials.other", Source: strata.SourceFile, RawValue: "private-value"})
	meta.Record(strata.Origin{Key: "credentials_public", Source: strata.SourceDefault, RawValue: "public-value"})

	for _, key := range []string{"credentials", "credentials.service", "credentials.other"} {
		origin, found := meta.Where(key)
		if !found || origin.RawValue != "[REDACTED]" {
			t.Errorf("Where(%s) = %+v, %t", key, origin, found)
		}
	}

	if origin, _ := meta.Where("credentials_public"); origin.RawValue != "public-value" {
		t.Errorf("unrelated value was redacted: %+v", origin)
	}
}

func TestSecretTagMasking(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "super-secret-api-key")

	_, meta, err := strata.LoadWithMetadata[secretConfig](
		strata.WithoutFileDiscovery(),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	orig, ok := meta.Where("token")
	if !ok {
		t.Fatalf("Where(token) not found")
	}

	if orig.RawValue != "[REDACTED]" {
		t.Errorf("RawValue = %q, want [REDACTED]", orig.RawValue)
	}
}

func TestFileSecretRedaction(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	filePath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(filePath, []byte(`{"token":"file-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, meta, err := strata.LoadWithMetadata[secretFileConfig](strata.WithPath(filePath), strata.WithFormats("json"))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	o, ok := meta.Where("token")
	if !ok {
		t.Fatal("missing origin")
	}

	if o.RawValue != "[REDACTED]" {
		t.Errorf("secret file origin leaked %q", o.RawValue)
	}

	msg := meta.NewConfigError("token", errors.New("invalid token")).Error()
	if strings.Contains(msg, "file-secret") {
		t.Errorf("validation error leaked file secret:\n%s", msg)
	}
}

type redactDatabase struct {
	Password string `strata:"password,secret"`
}

type redactCredentials struct {
	User string `strata:"user"`
	Pass string `strata:"pass"`
}

type redactConfig struct {
	Typed     strata.Secret     `strata:"typed"`
	Pointer   *strata.Secret    `strata:"pointer"`
	Tagged    string            `strata:"tagged,secret"`
	EnvTagged string            `env:"REDACT_ENV_TAGGED,secret" strata:"env_tagged"`
	Database  redactDatabase    `strata:"database"`
	Creds     redactCredentials `strata:"creds,secret"`
}

var redactKeys = []string{"typed", "pointer", "tagged", "env_tagged", "database.password", "creds.user", "creds.pass"}

// Distinct values per layer ensure a leak cannot hide behind another layer.
func TestSecretOriginsAreRedactedInEveryLayer(t *testing.T) {
	const leak = "leak-7f3a"

	toml := "typed = 'toml-" + leak + "'\npointer = 'toml-" + leak + "'\ntagged = 'toml-" + leak + "'\nenv_tagged = 'toml-" + leak +
		"'\n[database]\npassword = 'toml-" + leak + "'\n[creds]\nuser = 'toml-" + leak + "'\npass = 'toml-" + leak + "'\n"
	yaml := "typed: yaml-" + leak + "\npointer: yaml-" + leak + "\ntagged: yaml-" + leak + "\nenv_tagged: yaml-" + leak +
		"\ndatabase:\n  password: yaml-" + leak + "\ncreds:\n  user: yaml-" + leak + "\n  pass: yaml-" + leak + "\n"
	json := `{"typed": "json-` + leak + `", "pointer": "json-` + leak + `", "tagged": "json-` + leak + `", "env_tagged": "json-` + leak +
		`", "database": {"password": "json-` + leak + `"}, "creds": {"user": "json-` + leak + `", "pass": "json-` + leak + `"}}`
	pointerDefault := strata.Secret("default-" + leak)

	for _, tc := range []struct {
		name   string
		source strata.SourceKind
		opts   func(t *testing.T) []strata.Option
	}{
		{"default", strata.SourceDefault, func(*testing.T) []strata.Option {
			v := "default-" + leak

			return []strata.Option{strata.WithDefaults(redactConfig{
				Typed: strata.Secret(v), Pointer: &pointerDefault, Tagged: v, EnvTagged: v,
				Database: redactDatabase{Password: v}, Creds: redactCredentials{User: v, Pass: v},
			})}
		}},
		{"toml", strata.SourceFile, func(t *testing.T) []strata.Option {
			t.Helper()

			return []strata.Option{strata.WithPath(writeFile(t, "c.toml", toml)), strata.WithFormats("toml")}
		}},
		{"yaml", strata.SourceFile, func(t *testing.T) []strata.Option {
			t.Helper()

			return []strata.Option{strata.WithPath(writeFile(t, "c.yaml", yaml)), strata.WithFormats("yaml")}
		}},
		{"json", strata.SourceFile, func(t *testing.T) []strata.Option {
			t.Helper()

			return []strata.Option{strata.WithPath(writeFile(t, "c.json", json)), strata.WithFormats("json")}
		}},
		{"stdin", strata.SourceStdin, func(*testing.T) []strata.Option {
			return []strata.Option{strata.WithPath("-"), strata.WithStdin(strings.NewReader(toml)), strata.WithFormats("toml")}
		}},
		{"env", strata.SourceEnv, func(t *testing.T) []strata.Option {
			t.Helper()

			for _, name := range []string{"REDACT_TYPED", "REDACT_POINTER", "REDACT_TAGGED", "REDACT_ENV_TAGGED", "REDACT_DATABASE_PASSWORD", "REDACT_CREDS_USER", "REDACT_CREDS_PASS"} {
				t.Setenv(name, "env-"+leak)
			}

			return []strata.Option{strata.WithEnvPrefix("REDACT_")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, meta, err := strata.LoadWithMetadata[redactConfig](tc.opts(t)...)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if !strings.Contains(cfg.Creds.Pass, leak) {
				t.Fatalf("creds.pass = %q: the layer did not set the value", cfg.Creds.Pass)
			}

			for _, key := range redactKeys {
				origin, ok := meta.Where(key)
				if !ok || origin.Source != tc.source || origin.RawValue != "[REDACTED]" {
					t.Errorf("Where(%s) = %+v, %t; want %s with [REDACTED]", key, origin, ok, tc.source)
				}
			}

			for _, origin := range meta.Origins() {
				if strings.Contains(origin.RawValue, leak) {
					t.Errorf("Origins() leaks %s: %+v", origin.Key, origin)
				}
			}
		})
	}
}

func TestDecodeErrorsNameKeysWithoutSecretValues(t *testing.T) {
	t.Parallel()

	type config struct {
		Port int
		Pin  int `strata:"pin,secret"`
	}

	for _, test := range []struct {
		name, document string
		wantPort       string
	}{
		{name: "config.yaml", document: "port: busy\npin: s3cr3t-pin\n", wantPort: `line 1: key "port"`},
		{name: "config.json", document: "{\n  \"port\": \"busy\",\n  \"pin\": \"s3cr3t-pin\"\n}", wantPort: `line 2: key "port"`},
		{name: "config.toml", document: "port = \"busy\"\npin = \"s3cr3t-pin\"\n", wantPort: `key "port"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, test.name, test.document)

			_, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(filepath.Ext(test.name)))
			if !errors.Is(err, strata.ErrMalformed) {
				t.Fatalf("err = %v, want ErrMalformed", err)
			}

			message := err.Error()
			if strings.Contains(message, "s3cr3t") || !strings.Contains(message, test.wantPort) || !strings.Contains(message, `key "pin": invalid value [REDACTED]`) {
				t.Fatalf("err = %q, want the port failure in full and the pin value redacted", message)
			}
		})
	}
}

func TestUnattributedDecodeErrorsStayRedacted(t *testing.T) {
	t.Parallel()

	type config struct {
		Pin string `strata:"pin,secret"`
	}

	path := writeFile(t, "config.toml", "pin = s3cr3t-pin\n")

	_, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("toml"))
	if !errors.Is(err, strata.ErrMalformed) || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("err = %v, want a redacted malformed-document error", err)
	}
}

func TestDecodeErrorsRedactSecretsUnderDottedMapKeys(t *testing.T) {
	t.Parallel()

	type entry struct {
		Password time.Duration `strata:",secret"`
	}

	type config struct {
		Entries map[string]entry
	}

	for name, document := range map[string]string{
		"config.json": `{"entries": {"a.b": {"password": "s3cr3t-pin"}}}`,
		"config.toml": "[entries.\"a.b\"]\npassword = \"s3cr3t-pin\"\n",
		"config.yaml": "entries:\n  a.b:\n    password: s3cr3t-pin\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, name, document)

			_, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(filepath.Ext(name)))

			if !errors.Is(err, strata.ErrMalformed) || strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), `key "entries.a\.b.password": invalid value [REDACTED]`) {
				t.Fatalf("err = %v, want a redacted failure at entries.a\\.b.password", err)
			}
		})
	}
}

func TestInlineSecretStructsStayRedacted(t *testing.T) {
	t.Parallel()

	type credentials struct {
		Code int
	}

	type config struct {
		Credentials credentials `strata:",inline,secret"`
	}

	path := writeFile(t, "config.yaml", "code: s3cr3t-pin\n")

	_, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(yamlFormat))

	if !errors.Is(err, strata.ErrMalformed) || strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), `key "code": invalid value [REDACTED]`) {
		t.Fatalf("err = %v, want a redacted failure at /code", err)
	}
}

func TestInlineSecretStructOriginsStayRedacted(t *testing.T) {
	t.Parallel()

	type credentials struct {
		Code int
	}

	type config struct {
		Credentials credentials `yaml:",inline" strata:",secret"`
	}

	path := writeFile(t, "config.yaml", "code: 4321\n")

	got, meta, err := strata.LoadWithMetadata[config](strata.WithPath(path), strata.WithFormats(yamlFormat))
	if err != nil {
		t.Fatal(err)
	}

	if origin, found := meta.Where("code"); got.Credentials.Code != 4321 || !found || origin.RawValue != "[REDACTED]" {
		t.Fatalf("code = %d, Where(/code) = %+v, %t; want the value loaded and its origin redacted", got.Credentials.Code, origin, found)
	}
}

func TestSecretMapEntriesWithEscapedKeysStayRedacted(t *testing.T) {
	t.Parallel()

	type config struct {
		Tokens map[string]string `strata:",secret"`
	}

	path := writeFile(t, "config.yaml", "tokens:\n  a.b: s3cr3t-pin\n")

	if err := strata.Set[config](path, `tokens.c\.d`, "s3cr3t-new"); err != nil {
		t.Fatal(err)
	}

	got, meta, err := strata.LoadWithMetadata[config](strata.WithPath(path), strata.WithFormats(yamlFormat))
	if err != nil {
		t.Fatal(err)
	}

	if want := map[string]string{"a.b": "s3cr3t-pin", "c.d": "s3cr3t-new"}; !maps.Equal(got.Tokens, want) {
		t.Fatalf("tokens = %q, want %q", got.Tokens, want)
	}

	for _, key := range []string{`tokens.a\.b`, `tokens.c\.d`} {
		if origin, found := meta.Where(key); !found || origin.Key != key || origin.RawValue != "[REDACTED]" {
			t.Errorf("Where(%q) = %+v, %t; want a redacted origin under the same key", key, origin, found)
		}
	}
}
