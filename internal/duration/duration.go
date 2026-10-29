package duration

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

const (
	day  = 24 * time.Hour
	week = 7 * day
)

var (
	ErrEmpty = errors.New("empty duration string")

	unitPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]*)?|\.[0-9]+)([wWdD])`)
)

// Parse is [time.ParseDuration] extended with day (d) and week (w) units.
func Parse(s string) (time.Duration, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, ErrEmpty
	}

	if !strings.ContainsAny(trimmed, "wdWD") {
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return 0, fmt.Errorf("parse duration %q: %w", s, err)
		}

		return d, nil
	}

	converted := unitPattern.ReplaceAllStringFunc(trimmed, func(match string) string {
		numStr := match[:len(match)-1]
		unit := strings.ToLower(match[len(match)-1:])

		value, ok := new(big.Rat).SetString(numStr)
		if !ok {
			return match
		}

		var duration time.Duration

		switch unit {
		case "w":
			duration = week
		case "d":
			duration = day
		default:
			return match
		}

		value.Mul(value, new(big.Rat).SetInt64(int64(duration)))
		nanoseconds := new(big.Int).Quo(value.Num(), value.Denom())

		return nanoseconds.String() + "ns"
	})

	d, err := time.ParseDuration(converted)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", s, err)
	}

	return d, nil
}
