package strata

import "log/slog"

// Secret is a string value that redacts itself when formatted or logged.
// Use Value to retrieve the underlying string. Save writes the real value;
// Init omits the field from generated templates.
type Secret string

// String returns the redacted form of the secret.
func (s Secret) String() string { return "[REDACTED]" }

// GoString returns the redacted form used by %#v.
func (s Secret) GoString() string { return "[REDACTED]" }

// LogValue returns a redacted slog value.
func (s Secret) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

// Value returns the underlying secret string.
func (s Secret) Value() string { return string(s) }
