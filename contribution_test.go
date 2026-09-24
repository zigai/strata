package strata_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/zigai/strata"
)

type contributionValidatedConfig struct {
	Port int `yaml:"port"`
}

func (c *contributionValidatedConfig) Validate() error {
	if c.Port < 1024 {
		return fmt.Errorf("port must be privileged or above: got %d", c.Port)
	}

	return nil
}

func TestWithContributionOverridesLowerTiers(t *testing.T) {
	t.Parallel()

	filePath := writeFile(t, "config.yaml", "port: 8080\n")

	type simpleConfig struct {
		Port int `yaml:"port"`
	}

	target, meta, err := strata.LoadWithMetadata[simpleConfig](
		strata.WithPath(filePath),
		strata.WithContribution(func(target any, meta *strata.Metadata) error {
			cfg, ok := target.(*simpleConfig)
			if !ok {
				return errors.New("target is not *simpleConfig")
			}

			cfg.Port = 9090

			meta.Record(strata.Origin{
				Key:      "port",
				Source:   strata.SourceFlag,
				Path:     "port",
				Line:     0,
				RawValue: "9090",
			})

			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if target.Port != 9090 {
		t.Errorf("Port = %d, want 9090", target.Port)
	}

	origin, ok := meta.Where("port")
	if !ok {
		t.Fatal("Where(port) not found")
	}

	if origin.Source != strata.SourceFlag || origin.RawValue != "9090" {
		t.Errorf("origin = %+v, want SourceFlag and 9090", origin)
	}
}

func TestWithContributionCanSatisfyValidation(t *testing.T) {
	t.Parallel()

	filePath := writeFile(t, "invalid.yaml", "port: 80\n")

	_, err := strata.Load[contributionValidatedConfig](strata.WithPath(filePath))
	if err == nil {
		t.Fatal("expected validation failure without contribution")
	}

	target, err := strata.Load[contributionValidatedConfig](
		strata.WithPath(filePath),
		strata.WithContribution(func(target any, _ *strata.Metadata) error {
			cfg, ok := target.(*contributionValidatedConfig)
			if !ok {
				return errors.New("target is not *contributionValidatedConfig")
			}

			cfg.Port = 8080

			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Load with correcting contribution failed: %v", err)
	}

	if target.Port != 8080 {
		t.Errorf("target.Port = %d, want 8080", target.Port)
	}
}

func TestWithContributionCanViolateValidation(t *testing.T) {
	t.Parallel()

	filePath := writeFile(t, "valid.yaml", "port: 8080\n")

	_, err := strata.Load[contributionValidatedConfig](
		strata.WithPath(filePath),
		strata.WithContribution(func(target any, _ *strata.Metadata) error {
			cfg, ok := target.(*contributionValidatedConfig)
			if !ok {
				return errors.New("target is not *contributionValidatedConfig")
			}

			cfg.Port = 80

			return nil
		}),
	)
	if err == nil {
		t.Fatal("expected validation failure from invalid contribution")
	}
}

func TestLoadReturnsZeroValueAfterContributionError(t *testing.T) {
	t.Parallel()

	type simpleConfig struct {
		Port int `yaml:"port"`
	}

	errExpected := errors.New("contribution exploded")

	got, meta, err := strata.LoadWithMetadata[simpleConfig](
		strata.WithContribution(func(target any, _ *strata.Metadata) error {
			cfg, ok := target.(*simpleConfig)
			if !ok {
				return errors.New("target is not *simpleConfig")
			}

			cfg.Port = 9090

			return errExpected
		}),
	)
	if !errors.Is(err, errExpected) {
		t.Errorf("got err %v, want %v", err, errExpected)
	}

	if got != (simpleConfig{}) || meta != nil {
		t.Errorf("got (%+v, %v), want zero config and nil metadata", got, meta)
	}
}

func TestWithContributionMultipleInOrder(t *testing.T) {
	t.Parallel()

	type orderedConfig struct {
		Order []int
	}

	target, err := strata.Load[orderedConfig](
		strata.WithContribution(func(target any, _ *strata.Metadata) error {
			cfg, ok := target.(*orderedConfig)
			if !ok {
				return errors.New("target is not *orderedConfig")
			}

			cfg.Order = append(cfg.Order, 1)

			return nil
		}),
		strata.WithContribution(nil),
		strata.WithContribution(func(target any, _ *strata.Metadata) error {
			cfg, ok := target.(*orderedConfig)
			if !ok {
				return errors.New("target is not *orderedConfig")
			}

			cfg.Order = append(cfg.Order, 2)

			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !reflect.DeepEqual(target.Order, []int{1, 2}) {
		t.Errorf("Order = %v, want [1, 2]", target.Order)
	}
}
