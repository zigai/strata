package defaulter_test

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/zigai/strata/internal/defaulter"
)

type portNumber int

type level int

func (l level) String() string { return fmt.Sprintf("level-%d", int(l)) }

type ptrLevel int

func (l *ptrLevel) String() string { return fmt.Sprintf("ptr-%d", int(*l)) }

type failure int

func (f failure) Error() string { return fmt.Sprintf("failure(%d)", int(f)) }

type leafCorpus struct {
	Int      int            `strata:"int"`
	Small    int8           `strata:"small"`
	Wide     uint64         `strata:"wide"`
	Flag     bool           `strata:"flag"`
	Text     string         `strata:"text"`
	Ratio    float32        `strata:"ratio"`
	Exact    float64        `strata:"exact"`
	Huge     float64        `strata:"huge"`
	Nano     float64        `strata:"nano"`
	Nap      time.Duration  `strata:"nap"`
	Port     portNumber     `strata:"port"`
	Level    level          `strata:"level"`
	PtrLevel ptrLevel       `strata:"ptr_level"`
	Failing  failure        `strata:"failing"`
	Ref      *int           `strata:"ref"`
	RefNap   *time.Duration `strata:"ref_nap"`
	Names    []string       `strata:"names"`
}

func corpus() leafCorpus {
	ref := 5
	nap := 90 * time.Second

	return leafCorpus{
		Int:      -7,
		Small:    -128,
		Wide:     math.MaxUint64,
		Flag:     true,
		Text:     "hello",
		Ratio:    3.3,
		Exact:    3.3,
		Huge:     1e21,
		Nano:     0.000001,
		Nap:      10 * time.Second,
		Port:     8080,
		Level:    3,
		PtrLevel: 9,
		Failing:  failure(42),
		Ref:      &ref,
		RefNap:   &nap,
		Names:    []string{"a", "b"},
	}
}

func TestDefaultRawValuesMatchFormatting(t *testing.T) {
	t.Parallel()

	cfg := corpus()

	recorded := make(map[string]string)

	if err := defaulter.Apply(&cfg, func(key, rawVal string) {
		recorded[key] = rawVal
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	typ := reflect.TypeOf(cfg)
	val := reflect.ValueOf(cfg)
	checked := 0

	for i := range val.NumField() {
		sf := typ.Field(i)
		field := val.Field(i)
		key := defaulter.FieldKey(sf)

		if field.Kind() == reflect.Pointer {
			field = field.Elem()
		}

		want := fmt.Sprintf("%v", field.Interface())

		got, ok := recorded[key]
		if !ok {
			t.Errorf("no default recorded for %s", key)

			continue
		}

		if got != want {
			t.Errorf("%s: recorded %q, fmt says %q", key, got, want)
		}

		checked++
	}

	if checked != val.NumField() {
		t.Errorf("checked %d of %d fields", checked, val.NumField())
	}
}

func TestInterceptingTypesAreNotFormattedByKind(t *testing.T) {
	t.Parallel()

	cfg := corpus()
	recorded := make(map[string]string)

	if err := defaulter.Apply(&cfg, func(key, rawVal string) {
		recorded[key] = rawVal
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for key, want := range map[string]string{
		"nap":       "10s",
		"level":     "level-3",
		"ptr_level": "9", // String method on the pointer receiver, which %v ignores
		"failing":   "failure(42)",
	} {
		if got := recorded[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}
