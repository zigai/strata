package strata

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	hoursInDay  = 24.0
	hoursInWeek = 168.0
)

var (
	// ErrEmptyDuration is returned when a duration string is empty after
	// whitespace is trimmed.
	ErrEmptyDuration = errors.New("empty duration string")

	durUnitRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]*)?|\.[0-9]+)([wWdD])`)
)

// Duration wraps [time.Duration] and extends the textual form used by JSON,
// TOML, and YAML with the day (d) and week (w) units.
//
// Parsing accepts more than formatting reproduces. Any text
// [Duration.UnmarshalText] accepts decodes to the matching [time.Duration], but
// [Duration.String] and [Duration.MarshalText] emit the underlying
// [time.Duration] representation. A value written as "7d" re-encodes as
// "168h0m0s": the number of seconds is preserved, the unit is not.
type Duration time.Duration

// ParseDuration parses a duration string.
//
// Standard units (ns, us, µs, ms, s, m, h) are handled by [time.ParseDuration].
// This function extends that grammar with days (d) and weeks (w), which makes
// "7d", "2w", "1w2d", "1.5d", and "-7d" valid. A leading "+" is accepted. The d
// and w unit letters are matched case-insensitively.
//
// Leading and trailing whitespace is trimmed before parsing. It returns
// [ErrEmptyDuration] if the trimmed input is empty. Any other failure returns
// the error from [time.ParseDuration] wrapped with the original input.
func ParseDuration(s string) (time.Duration, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, ErrEmptyDuration
	}

	if !strings.ContainsAny(trimmed, "wdWD") {
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return 0, fmt.Errorf("parse duration %q: %w", s, err)
		}

		return d, nil
	}

	converted := durUnitRe.ReplaceAllStringFunc(trimmed, func(match string) string {
		numStr := match[:len(match)-1]
		unit := strings.ToLower(match[len(match)-1:])

		val, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return match
		}

		var hours float64

		switch unit {
		case "w":
			hours = val * hoursInWeek
		case "d":
			hours = val * hoursInDay
		default:
			return match
		}

		return strconv.FormatFloat(hours, 'f', -1, 64) + "h"
	})

	d, err := time.ParseDuration(converted)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", s, err)
	}

	return d, nil
}

// Duration returns the underlying [time.Duration].
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// String returns the [time.Duration] representation.
//
// The unit a duration was parsed from is not reproduced: a value parsed from
// "7d" formats as "168h0m0s". The result is intended for display only. A caller
// that needs the original text is responsible for keeping it.
func (d Duration) String() string {
	return time.Duration(d).String()
}

// MarshalText implements [encoding.TextMarshaler].
//
// The encoded form is [Duration.String], which is not the inverse of
// [Duration.UnmarshalText]: a value configured as "2w" is encoded as "336h0m0s".
// The duration itself is preserved exactly.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
//
// It accepts the same grammar as [ParseDuration], including the day and week
// units that [time.ParseDuration] rejects. It returns the error from
// [ParseDuration] if the text cannot be parsed.
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := ParseDuration(string(text))
	if err != nil {
		return err
	}

	*d = Duration(parsed)

	return nil
}
