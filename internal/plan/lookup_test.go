package plan_test

import (
	"encoding/json/jsontext"
	"errors"
	"reflect"
	"testing"

	"github.com/zigai/strata/internal/keypath"
	"github.com/zigai/strata/internal/plan"
)

type lookupNested struct {
	MaxConns int
	Password string `strata:",secret"`
}

type lookupServer struct {
	Host string
}

type lookupRoot struct {
	DB      *lookupNested
	Enabled bool
	Server  lookupServer      `strata:",inline"`
	Labels  map[string]int    `strata:"labels"`
	Extra   map[string]string `yaml:",inline"`
}

func TestLeafFor(t *testing.T) {
	for _, test := range []struct {
		key    string
		name   string
		kind   plan.Kind
		secret bool
	}{
		{"db.max_conns", "db-max-conns", plan.KindInt, false},
		{"db.password", "db-password", plan.KindString, true},
		{"enabled", "enabled", plan.KindBool, false},
		{"host", "host", plan.KindString, false},
	} {
		target, err := plan.LeafFor(reflect.TypeFor[lookupRoot](), test.key)
		if err != nil {
			t.Fatal(err)
		}

		if target.FlagName != test.name || target.Kind != test.kind || target.IsSecret != test.secret {
			t.Fatalf("%s: %+v", test.key, target)
		}
	}

	for _, key := range []string{"db.missing", "server.host", "db", "labels.a", "db/max_conns"} {
		if _, err := plan.LeafFor(reflect.TypeFor[lookupRoot](), key); err == nil {
			t.Errorf("LeafFor(%q) was accepted", key)
		}
	}

	for key, want := range map[string]error{"": keypath.ErrEmpty, `db.max\_conns`: keypath.ErrInvalid} {
		if _, err := plan.LeafFor(reflect.TypeFor[lookupRoot](), key); !errors.Is(err, want) {
			t.Errorf("LeafFor(%q) error = %v, want %v", key, err, want)
		}
	}
}

func TestFieldTypeFollowsMapsAndInlineMaps(t *testing.T) {
	for _, test := range []struct {
		key  jsontext.Pointer
		want reflect.Type
	}{
		{"/labels/a.b", reflect.TypeFor[int]()},
		{"/labels/", reflect.TypeFor[int]()},
		{"/custom", reflect.TypeFor[string]()},
		{"/host", reflect.TypeFor[string]()},
	} {
		got, err := plan.FieldType(reflect.TypeFor[lookupRoot](), test.key)
		if err != nil || got != test.want {
			t.Errorf("FieldType(%q) = %v, %v; want %v", test.key, got, err, test.want)
		}
	}

	if _, err := plan.FieldType(reflect.TypeFor[lookupRoot](), "/labels/a/b"); err == nil {
		t.Error("FieldType ran through an int map entry")
	}
}
