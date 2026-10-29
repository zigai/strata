package keypath_test

import (
	"encoding/json/jsontext"
	"errors"
	"slices"
	"testing"

	"github.com/zigai/strata/internal/keypath"
)

func TestParseSplitsOnUnescapedDots(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		key  string
		want []string
	}{
		{"port", []string{"port"}},
		{"db.port", []string{"db", "port"}},
		{`labels.a\.b`, []string{"labels", "a.b"}},
		{`labels.a\\b`, []string{"labels", `a\b`}},
		{`labels.a\\.b`, []string{"labels", `a\`, "b"}},
		{`labels.a\\\.b`, []string{"labels", `a\.b`}},
		{"labels.", []string{"labels", ""}},
		{".a", []string{"", "a"}},
		{"a..b", []string{"a", "", "b"}},
		{"labels.a/b", []string{"labels", "a/b"}},
		{"labels.~0~1", []string{"labels", "~0~1"}},
		{"labels. spaced ", []string{"labels", " spaced "}},
		{"items.0", []string{"items", "0"}},
		{"étagère.ǆ", []string{"étagère", "ǆ"}},
	} {
		got, err := keypath.Parse(test.key)
		if err != nil {
			t.Errorf("Parse(%q) error = %v", test.key, err)
			continue
		}

		if tokens := slices.Collect(got.Tokens()); !slices.Equal(tokens, test.want) {
			t.Errorf("Parse(%q) tokens = %q, want %q", test.key, tokens, test.want)
		}
	}
}

func TestParseRejectsMalformedKeys(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		key  string
		want error
	}{
		{"", keypath.ErrEmpty},
		{`a\`, keypath.ErrInvalid},
		{`a\\\`, keypath.ErrInvalid},
		{`a\x`, keypath.ErrInvalid},
		{`a\/b`, keypath.ErrInvalid},
		{`a\ b`, keypath.ErrInvalid},
		{"a.\xff", keypath.ErrInvalid},
	} {
		if _, err := keypath.Parse(test.key); !errors.Is(err, test.want) {
			t.Errorf("Parse(%q) error = %v, want %v", test.key, err, test.want)
		}
	}
}

func TestFormatEscapesDotsAndBackslashes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		tokens []string
		want   string
	}{
		{[]string{"db", "port"}, "db.port"},
		{[]string{"labels", "a.b"}, `labels.a\.b`},
		{[]string{"labels", `a\b`}, `labels.a\\b`},
		{[]string{"labels", ""}, "labels."},
		{[]string{"labels", "a/b", "~1"}, "labels.a/b.~1"},
		{[]string{"labels", " x "}, "labels. x "},
	} {
		var key jsontext.Pointer
		for _, token := range test.tokens {
			key = key.AppendToken(token)
		}

		if got := keypath.Format(key); got != test.want {
			t.Errorf("Format(%q) = %q, want %q", test.tokens, got, test.want)
		}
	}
}

func FuzzFormatThenParseKeepsEveryToken(f *testing.F) {
	f.Add("labels", "a.b", `c\d`)
	f.Add("", "", "")
	f.Add("a/b", "~", ` .\. `)

	f.Fuzz(func(t *testing.T, first, second, third string) {
		var key jsontext.Pointer
		for _, token := range []string{first, second, third} {
			key = key.AppendToken(token)
		}

		if !key.IsValid() {
			t.Skip("tokens are not valid UTF-8")
		}

		got, err := keypath.Parse(keypath.Format(key))
		if err != nil || got != key {
			t.Fatalf("Parse(Format(%q)) = %q, %v", key, got, err)
		}
	})
}
