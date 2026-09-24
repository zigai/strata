package codec_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zigai/strata/codec"
)

// bindingPromoted is embedded in bindingConfig, so its fields are reachable from
// the struct that promotes them. Its type is unexported and its field is not: an
// embedded struct type is promoted whatever its own visibility.
type bindingPromoted struct {
	Promoted int `strata:"promoted"`
}

// BindingPointer is embedded in bindingConfig as a pointer, so its fields are
// promoted through a pointer that decoding allocates when a document names one
// of them.
type BindingPointer struct {
	Allocated int `strata:"allocated"`
}

// bindingNested is the element type of the nested fields bindingConfig carries.
type bindingNested struct {
	MaxConns int `strata:"max_conns"`
}

// bindingConfig names its fields the way the package names configuration keys: a
// strata, toml, or yaml tag where one is declared, and the snake_case form of the
// Go name otherwise. Only JSONKey declares a json tag, and only Skipped is
// excluded from the key model.
type bindingConfig struct {
	bindingPromoted
	*BindingPointer

	StrataKey  string `strata:"strata_key"`
	TOMLKey    string `toml:"toml_key"`
	YAMLKey    string `yaml:"yaml_key"`
	JSONKey    string `json:"json_key"`
	Untagged   int
	Skipped    int                      `strata:"-"`
	HTTPServer string                   `strata:"http_server"`
	Big        int64                    `strata:"big"`
	Nested     bindingNested            `strata:"nested"`
	Items      []bindingNested          `strata:"items"`
	Lookup     map[string]bindingNested `strata:"lookup"`
	Excluded   int                      `json:"-"`
}

// A JSON member MUST reach a field by the name encoding/json matches and by the
// configuration key the rest of the package derives for it, so a document
// written in the key style provenance reports loads into the struct it belongs
// to. Matching one and not the other was a silent no-op: the document loaded,
// the field kept its default, and the origin of the key was recorded anyway.
func TestJSONCodecBindsConfigurationKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		document string
		want     bindingConfig
	}{
		{
			"snake_case of an untagged field",
			`{"untagged": 1}`,
			bindingConfig{Untagged: 1},
		},
		{
			"strata key",
			`{"strata_key": "a"}`,
			bindingConfig{StrataKey: "a"},
		},
		{
			"toml key",
			`{"toml_key": "b"}`,
			bindingConfig{TOMLKey: "b"},
		},
		{
			"yaml key",
			`{"yaml_key": "c"}`,
			bindingConfig{YAMLKey: "c"},
		},
		{
			"json tag",
			`{"json_key": "d"}`,
			bindingConfig{JSONKey: "d"},
		},
		{
			"Go name is not a key",
			`{"Untagged": 2}`,
			bindingConfig{},
		},
		{
			"acronym",
			`{"http_server": "e"}`,
			bindingConfig{HTTPServer: "e"},
		},
		{
			"nested object",
			`{"nested": {"max_conns": 3}}`,
			bindingConfig{Nested: bindingNested{MaxConns: 3}},
		},
		{
			"slice elements",
			`{"items": [{"max_conns": 4}, {}]}`,
			bindingConfig{Items: []bindingNested{{MaxConns: 4}, {}}},
		},
		{
			"map values",
			`{"lookup": {"primary": {"max_conns": 5}}}`,
			bindingConfig{Lookup: map[string]bindingNested{"primary": {MaxConns: 5}}},
		},
		{
			"promoted field",
			`{"promoted": 6}`,

			//nolint:modernize // Go allows eliding a literal's type only in array, slice, and map elements, not in a struct field
			bindingConfig{bindingPromoted: bindingPromoted{Promoted: 6}},
		},
		{
			"promoted field through an embedded pointer",
			`{"allocated": 11}`,
			bindingConfig{BindingPointer: &BindingPointer{Allocated: 11}},
		},
		{
			"exact digits",
			`{"big": 9007199254740993}`,
			bindingConfig{Big: 9007199254740993},
		},
		{
			"member no field declares",
			`{"untagged": 7, "unknown": {"deep": 1}}`,
			bindingConfig{Untagged: 7},
		},
		{
			"field its strata key excludes",
			`{"skipped": 9}`,
			bindingConfig{},
		},
		{
			"field its json tag excludes",
			`{"excluded": 8, "Excluded": 8}`,
			bindingConfig{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var target bindingConfig

			if err := codec.NewJSONCodec().Decode([]byte(tc.document), &target); err != nil {
				t.Fatalf("Decode(%s) error: %v", tc.document, err)
			}

			if !reflect.DeepEqual(target, tc.want) {
				t.Errorf("Decode(%s) = %+v, want %+v", tc.document, target, tc.want)
			}
		})
	}
}

// A document overlays the value it is decoded into: a field the document omits
// keeps the value it holds, whatever the rewrite did to the members around it.
func TestJSONCodecKeepsOmittedFields(t *testing.T) {
	t.Parallel()

	target := bindingConfig{
		StrataKey: "kept",
		Nested:    bindingNested{MaxConns: 1},
		Items:     []bindingNested{{MaxConns: 2}},
	}

	if err := codec.NewJSONCodec().Decode([]byte(`{"toml_key": "set"}`), &target); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	want := bindingConfig{
		StrataKey: "kept",
		TOMLKey:   "set",
		Nested:    bindingNested{MaxConns: 1},
		Items:     []bindingNested{{MaxConns: 2}},
	}

	if !reflect.DeepEqual(target, want) {
		t.Errorf("Decode = %+v, want %+v", target, want)
	}
}

// Only the configuration key names a field. A member spelled the way
// encoding/json would match on its own is ignored, so it cannot compete with the
// key for the same field.
func TestJSONCodecIgnoresOtherSpellings(t *testing.T) {
	t.Parallel()

	var target bindingNested

	if err := codec.NewJSONCodec().Decode([]byte(`{"max_conns": 1, "MaxConns": 2, "maxconns": 3}`), &target); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if target.MaxConns != 1 {
		t.Errorf("MaxConns = %d, want 1 from the configuration key", target.MaxConns)
	}
}

// Rewriting a document MUST NOT weaken what encoding/json rejects: a duplicate
// member, invalid UTF-8, and a syntax error each stay the parse failures they
// were, whatever the rewrite would have done with the members around them.
func TestJSONCodecKeepsParseFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		document string
	}{
		{"duplicate member", `{"max_conns": 1, "max_conns": 2}`},
		{"invalid UTF-8", "{\"max_conns\": \"x\xffy\"}"},
		{"unclosed object", `{"max_conns": 1`},
		{"value missing", `{"max_conns": }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var target bindingNested

			err := codec.NewJSONCodec().Decode([]byte(tc.document), &target)
			if err == nil {
				t.Fatalf("Decode accepted %s and produced %+v", tc.document, target)
			}

			if !errors.Is(err, codec.ErrMalformed) {
				t.Errorf("errors.Is(err, ErrMalformed) = false (err = %v)", err)
			}
		})
	}
}

// verbatimDocument records the JSON text it is handed, so a test can tell that a
// type which decodes itself was given its members exactly as the document wrote
// them.
type verbatimDocument struct {
	text string
}

func (v *verbatimDocument) UnmarshalJSON(data []byte) error {
	v.text = string(data)

	return nil
}

// A type that decodes itself names its own members, so the rewrite stops at it:
// the field around it is renamed to the name encoding/json matches, and its value
// reaches the decoder as the document wrote it.
func TestJSONCodecLeavesSelfDecodingValuesAlone(t *testing.T) {
	t.Parallel()

	var target struct {
		Own verbatimDocument `strata:"own"`
	}

	if err := codec.NewJSONCodec().Decode([]byte(`{"own": {"inner_value": 1}}`), &target); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if want := `{"inner_value": 1}`; target.Own.text != want {
		t.Errorf("the decoder received %s, want %s", target.Own.text, want)
	}
}
