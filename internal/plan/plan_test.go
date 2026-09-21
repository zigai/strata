package plan_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/strata/internal/plan"
)

type credentials struct {
	Token string `flag:"token"`
}

// plain is reached only through the container policy that registration does not
// use, because it carries no tag of its own.
type plain struct {
	Mode string `flag:"mode"`
}

type settings struct {
	Name  string       `flag:"name"`
	Creds *credentials `flag:"creds"`
	Count int          `flag:"count"`
	Plain plain
}

// Build reports the shape of a struct without touching a value, so a bridge can
// register flags before any configuration is loaded.
//
// A nested struct that carries a tag of its own is recursed, and its leaves take
// the container's flag name as a prefix. A nested struct with no tag is not
// reached by the registration policy.
func TestBuildFollowsTaggedContainersOnly(t *testing.T) {
	t.Parallel()

	names := flagNames(t, plan.Registration)

	for _, want := range []string{"name", "count", "creds-token"} {
		if !contains(names, want) {
			t.Errorf("no target named %q in %v", want, names)
		}
	}

	if contains(names, "plain-mode") {
		t.Errorf("Registration followed an untagged container: %v", names)
	}
}

// The apply policy widens the walk to untagged containers, so a flag derived from
// a field name still matches a value supplied before the tag existed.
func TestBuildFollowsUntaggedContainersUnderTheApplyPolicy(t *testing.T) {
	t.Parallel()

	if names := flagNames(t, plan.Apply); !contains(names, "plain-mode") {
		t.Errorf("Apply did not follow an untagged container: %v", names)
	}
}

func flagNames(t *testing.T, policy plan.Policy) []string {
	t.Helper()

	targets, err := plan.Build(reflect.TypeFor[settings](), policy)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	names := make([]string, 0, len(targets))

	for _, target := range targets {
		names = append(names, target.Name)
	}

	return names
}

// EnsureField allocates a nil pointer along the path, which is the write path for
// an optional subtree the configuration left unset and a CLI value supplies.
func TestEnsureFieldAllocatesAlongThePath(t *testing.T) {
	t.Parallel()

	value := settings{}

	resolved, err := plan.EnsureField(reflect.ValueOf(&value).Elem(), []int{1, 0})
	if err != nil {
		t.Fatalf("EnsureField: %v", err)
	}

	if resolved.Kind() != reflect.String || !resolved.CanSet() {
		t.Fatalf("resolved %s, want a settable string", resolved.Kind())
	}

	resolved.SetString("from-a-flag")

	if value.Creds == nil || value.Creds.Token != "from-a-flag" {
		t.Errorf("value = %+v, want the nested pointer allocated and written", value)
	}
}

// A path that does not lead through struct fields is reported as ErrNotStruct.
//
// Build produces only paths that do, so this pins the contract for a caller that
// assembles a Target itself.
func TestEnsureFieldReportsAPathThatLeavesTheStructs(t *testing.T) {
	t.Parallel()

	value := settings{}

	// Field 2 is an int, so the next step cannot descend into it.
	_, err := plan.EnsureField(reflect.ValueOf(&value).Elem(), []int{2, 0})
	if !errors.Is(err, plan.ErrNotStruct) {
		t.Fatalf("err = %v, want ErrNotStruct", err)
	}

	if !strings.Contains(err.Error(), "[2]") {
		t.Errorf("err = %v, want it to name the path segment that failed", err)
	}
}

// A nil container that cannot be allocated is reported rather than silently
// leaving the caller to write into an invalid value.
func TestEnsureFieldReportsAnUnsettableContainer(t *testing.T) {
	t.Parallel()

	// A value obtained from a non-addressable struct cannot set its fields.
	value := reflect.ValueOf(settings{})

	_, err := plan.EnsureField(value, []int{1, 0})
	if !errors.Is(err, plan.ErrNotStruct) {
		t.Fatalf("err = %v, want ErrNotStruct", err)
	}
}

// ResolveField reports an absent optional subtree with false rather than an
// error, because a nil pointer is a legitimate state to read from.
func TestResolveFieldReportsAMissingSubtree(t *testing.T) {
	t.Parallel()

	value := settings{}

	if _, ok := plan.ResolveField(reflect.ValueOf(&value).Elem(), []int{1, 0}); ok {
		t.Error("ResolveField reported a value for an unset subtree")
	}

	value.Creds = &credentials{Token: "present"}

	resolved, ok := plan.ResolveField(reflect.ValueOf(&value).Elem(), []int{1, 0})
	if !ok {
		t.Fatal("ResolveField missed a present subtree")
	}

	if resolved.String() != "present" {
		t.Errorf("resolved %q, want present", resolved.String())
	}
}

// Build refuses a root that is not a struct, which is the first thing a bridge
// checks about the value it was handed.
func TestBuildRequiresAStructRoot(t *testing.T) {
	t.Parallel()

	if _, err := plan.Build(reflect.TypeFor[int](), plan.Registration); !errors.Is(err, plan.ErrNotStruct) {
		t.Errorf("err = %v, want ErrNotStruct", err)
	}
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

func TestNestedSecretFlagPlan(t *testing.T) {
	type creds struct {
		Token string `flag:"token"`
	}
	type cfg struct {
		Auth creds `flag:"auth" strata:"auth,secret"`
	}
	targets, err := plan.Build(reflect.TypeFor[cfg](), plan.Registration)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets=%#v", targets)
	}
	if !targets[0].Secret {
		t.Errorf("parent secret tag lost: --%s has Secret=false", targets[0].Name)
	}
}

type customText struct{ N int }

func (v *customText) MarshalText() ([]byte, error) { return []byte(fmt.Sprintf("n=%d", v.N)), nil }
func (v *customText) UnmarshalText(s []byte) error {
	_, err := fmt.Sscanf(string(s), "n=%d", &v.N)
	return err
}

func TestPointerMarshalerApplyByValue(t *testing.T) {
	cfg := struct {
		Value customText `flag:"value"`
	}{Value: customText{N: 42}}
	root, ok := plan.OptionalStructTarget(cfg)
	if !ok {
		t.Fatal("target rejected")
	}
	targets, err := plan.Build(root.Type(), plan.Apply)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := plan.ResolveField(root, targets[0].IndexPath)
	if !ok {
		t.Fatal("not resolved")
	}
	text, err := plan.EncodeScalar(source, targets[0].Kind)
	if err != nil {
		t.Fatal(err)
	}
	if text != "n=42" {
		t.Errorf("by-value Apply renders %q; MarshalText requires %q", text, "n=42")
	}
}
