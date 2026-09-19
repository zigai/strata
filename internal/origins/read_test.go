package origins_test

import (
	"testing"

	"github.com/zigai/strata/internal/origins"
)

// collect runs Read and returns the records indexed by key.
func collect(t *testing.T, data, format string) map[string]origins.Record {
	t.Helper()

	records := make(map[string]origins.Record)

	origins.Read([]byte(data), format, func(record origins.Record) {
		records[record.Key] = record
	})

	return records
}

const nested = `{
  "server": {"port": 8080},
  "name": "app"
}`

// A nested mapping contributes one record per leaf, with the segments joined by
// a dot, matching the key every other tier records.
func TestReadJoinsNestedKeys(t *testing.T) {
	t.Parallel()

	records := collect(t, nested, ".json")

	for _, key := range []string{"server.port", "name"} {
		if _, ok := records[key]; !ok {
			t.Errorf("no record for %q, got %v", key, keysOf(records))
		}
	}

	if len(records) != 2 {
		t.Errorf("reported %d records, want 2: %v", len(records), keysOf(records))
	}
}

// Only the YAML reader walks a node tree, so only it can report a line.
func TestReadReportsLinesForYAMLOnly(t *testing.T) {
	t.Parallel()

	yamlRecords := collect(t, "server:\n  port: 8080\nname: app\n", ".yaml")

	if got := yamlRecords["server.port"].Line; got != 2 {
		t.Errorf("YAML server.port line = %d, want 2", got)
	}

	for _, format := range []string{".toml", ".json"} {
		body := map[string]string{
			".toml": "name = \"app\"\n",
			".json": `{"name": "app"}`,
		}[format]

		if got := collect(t, body, format)["name"].Line; got != 0 {
			t.Errorf("%s reported line %d, want 0", format, got)
		}
	}

	// Formats without a leading dot should select the correct reader directly
	yamlNoDot := collect(t, "server:\n  port: 8080\nname: app\n", "yaml")
	if got := yamlNoDot["server.port"].Line; got != 2 {
		t.Errorf("YAML (no dot) server.port line = %d, want 2", got)
	}
}

// A number is reported as the document wrote it. Routing it through float64
// would render this value as 9.007199254740992e+15.
func TestReadKeepsExactNumberLiterals(t *testing.T) {
	t.Parallel()

	const body = `{"big": 9007199254740993}`

	if got := collect(t, body, ".json")["big"].RawValue; got != "9007199254740993" {
		t.Errorf("JSON big = %q, want the literal", got)
	}

	if got := collect(t, "big = 9007199254740993\n", ".toml")["big"].RawValue; got != "9007199254740993" {
		t.Errorf("TOML big = %q, want the literal", got)
	}
}

func TestReadUnquotesStrings(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ format, body string }{
		{".json", `{"name": "app"}`},
		{".yaml", "name: app\n"},
		{".toml", "name = \"app\"\n"},
	} {
		if got := collect(t, tc.body, tc.format)["name"].RawValue; got != "app" {
			t.Errorf("%s name = %q, want app unquoted", tc.format, got)
		}
	}
}

// A format with no reader of its own is tried as TOML, then YAML, then JSON.
func TestReadIdentifiesAnUnknownFormat(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{"toml", "name = \"app\"\n"},
		{"yaml", "name: app\n"},
		{"json", `{"name": "app"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := collect(t, tc.body, "")["name"].RawValue; got != "app" {
				t.Errorf("name = %q, want app", got)
			}
		})
	}
}

// Read never reports an error: provenance describes a document a codec has
// already accepted. A document no reader understands reports nothing.
func TestReadReportsNothingForUnreadableInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ format, body string }{
		{".json", "not json at all"},
		{".yaml", "\t\t\t"},
		{".toml", "= = ="},
	} {
		if records := collect(t, tc.body, tc.format); len(records) != 0 {
			t.Errorf("%s reported %d records for unreadable input: %v",
				tc.format, len(records), keysOf(records))
		}
	}
}

func keysOf(records map[string]origins.Record) []string {
	keys := make([]string, 0, len(records))

	for key := range records {
		keys = append(keys, key)
	}

	return keys
}
