package main

import (
	"errors"
	"time"

	"github.com/zigai/strata"
)

type Config struct {
	Env     string
	Port    int
	Timeout strata.Duration
	Verbose bool
	DB      Database
	APIKey  strata.Secret
}

type Database struct {
	Host     string
	Port     int
	MaxConns int
}

func (c *Config) SetDefaults() {
	c.Env = "development"
	c.Port = 8080
	c.Timeout = strata.Duration(30 * time.Second)
	c.DB.Host = "localhost"
	c.DB.Port = 5432
	c.DB.MaxConns = 20
}

func (c *Config) ValidateWith(meta *strata.Metadata) error {
	var errs []error
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, meta.NewConfigError("port", errors.New("must be between 1 and 65535")))
	}
	if c.DB.Port < 1 || c.DB.Port > 65535 {
		errs = append(errs, meta.NewConfigError("db.port", errors.New("must be between 1 and 65535")))
	}
	if c.Env == "production" && c.APIKey == "" {
		errs = append(errs, meta.NewConfigError("env", errors.New("requires api_key to be set")))
	}
	return errors.Join(errs...)
}
