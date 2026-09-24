package plan_test

import (
	"reflect"
	"testing"

	"github.com/zigai/strata/internal/defaulter"
	"github.com/zigai/strata/internal/plan"
)

type lookupNested struct {
	MaxConns int
	Password string `strata:",secret"`
}

type lookupRoot struct {
	DB      *lookupNested
	Enabled bool
}

func TestLookup(t *testing.T) {
	for _, test := range []struct {
		key    string
		name   string
		kind   plan.Kind
		secret bool
	}{
		{"db.max_conns", "db-max-conns", plan.KindInt, false},
		{"db.password", "db-password", plan.KindString, true},
		{"enabled", "enabled", plan.KindBool, false},
	} {
		target, err := plan.Lookup(reflect.TypeFor[lookupRoot](), test.key)
		if err != nil {
			t.Fatal(err)
		}

		if target.Name != test.name || target.Kind != test.kind || target.Secret != test.secret {
			t.Fatalf("%s: %+v", test.key, target)
		}
	}

	if _, err := plan.Lookup(reflect.TypeFor[lookupRoot](), "db.missing"); err == nil {
		t.Fatal("missing key was accepted")
	}

	if got := defaulter.FieldKey(reflect.TypeFor[lookupNested]().Field(0)); got != "max_conns" {
		t.Fatalf("field key = %q", got)
	}
}
