package strata_test

// Fixtures shared by the tests in this package. A fixture that more than one
// file uses MUST be declared once, here, so every file sees the same type.

// narrowConfig carries integer and float fields narrower than the values the
// env tests feed them, so a conversion that wraps or saturates stays observable.
type narrowConfig struct {
	Small int8    `strata:"small" env:"NARROW_SMALL"`
	Tiny  uint8   `strata:"tiny" env:"NARROW_TINY"`
	Float float32 `strata:"float" env:"NARROW_FLOAT"`
}

// wideConfig carries one int64 field, used where a value's exact width matters
// to the provenance and file-size tests.
type wideConfig struct {
	Small int64 `strata:"small" json:"small" toml:"small" yaml:"small"`
}

// demoConfig is the round-trip fixture behind the schema, init, and save tests.
type demoConfig struct {
	ServerHost string `json:"server_host" toml:"server_host" yaml:"server_host"`
	ServerPort int    `json:"server_port" toml:"server_port" yaml:"server_port"`
	Debug      bool   `json:"debug"       toml:"debug"       yaml:"debug"`
}

func (d *demoConfig) SetDefaults() {
	d.ServerHost = "127.0.0.1"
	d.ServerPort = 8080
	d.Debug = true
}
