package strata_test

import (
	"testing"
	"time"

	"github.com/zigai/strata"
)

func TestParseDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: true,
		},
		{
			name:  "standard milliseconds",
			input: "300ms",
			want:  300 * time.Millisecond,
		},
		{
			name:  "standard seconds",
			input: "45s",
			want:  45 * time.Second,
		},
		{
			name:  "standard compound",
			input: "1h30m",
			want:  90 * time.Minute,
		},
		{
			name:  "days lowercase",
			input: "7d",
			want:  7 * 24 * time.Hour,
		},
		{
			name:  "days uppercase",
			input: "7D",
			want:  7 * 24 * time.Hour,
		},
		{
			name:  "weeks lowercase",
			input: "2w",
			want:  2 * 7 * 24 * time.Hour,
		},
		{
			name:  "weeks uppercase",
			input: "2W",
			want:  2 * 7 * 24 * time.Hour,
		},
		{
			name:  "fractional days",
			input: "1.5d",
			want:  36 * time.Hour,
		},
		{
			name:  "fractional weeks",
			input: "0.5w",
			want:  84 * time.Hour,
		},
		{
			name:  "compound weeks and days",
			input: "1w2d",
			want:  (7 + 2) * 24 * time.Hour,
		},
		{
			name:  "compound weeks days hours",
			input: "1w1d12h",
			want:  (8*24 + 12) * time.Hour,
		},
		{
			name:  "tiny fractional days",
			input: "0.0000001d",
			want:  8640 * time.Microsecond,
		},
		{
			name:  "negative days",
			input: "-7d",
			want:  -7 * 24 * time.Hour,
		},
		{
			name:  "negative weeks",
			input: "-2w",
			want:  -2 * 7 * 24 * time.Hour,
		},
		{
			name:  "fractional days without leading zero",
			input: ".5d",
			want:  12 * time.Hour,
		},
		{
			name:  "negative fractional days without leading zero",
			input: "-.5d",
			want:  -12 * time.Hour,
		},
		{
			name:  "fractional weeks without leading zero",
			input: ".5w",
			want:  84 * time.Hour,
		},
		{
			name:  "trailing dot days",
			input: "1.d",
			want:  24 * time.Hour,
		},
		{
			name:    "invalid unit",
			input:   "10x",
			wantErr: true,
		},
		{
			name:    "invalid string",
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := strata.ParseDuration(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseDuration(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}

			if !tc.wantErr && got != tc.want {
				t.Fatalf("ParseDuration(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestDurationTextUnmarshaler(t *testing.T) {
	t.Parallel()

	var d strata.Duration
	if err := d.UnmarshalText([]byte("7d")); err != nil {
		t.Fatalf("UnmarshalText error: %v", err)
	}

	want := 7 * 24 * time.Hour
	if d.Duration() != want {
		t.Fatalf("Duration() = %v, want %v", d.Duration(), want)
	}

	text, err := d.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText error: %v", err)
	}

	if string(text) != want.String() {
		t.Fatalf("MarshalText() = %s, want %s", string(text), want.String())
	}
}
