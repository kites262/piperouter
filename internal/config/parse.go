package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse decodes data as a strict YAML configuration document (PRD §6.3, §6.7).
//
// Unknown fields are rejected (yaml.v3 KnownFields) and any version other
// than SupportedVersion is rejected. Empty input decodes to a zero config
// whose version (0) then fails the version check. On success the returned
// config has been normalized (all defaults filled in). Parse does NOT run
// Validate; callers decide when to validate.
func Parse(data []byte) (*Config, error) {
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		var typeErr *yaml.TypeError
		if errors.As(err, &typeErr) && hasUnknownField(typeErr) {
			return nil, fmt.Errorf("unknown configuration field: %w", err)
		}
		return nil, fmt.Errorf("parse configuration: %w", err)
	}
	if c.Version != SupportedVersion {
		return nil, fmt.Errorf("unsupported configuration version %d (supported version is %d)", c.Version, SupportedVersion)
	}
	c.Normalize()
	return c, nil
}

// hasUnknownField reports whether the yaml.v3 type error was caused by a
// field that does not exist in the schema (KnownFields strict mode).
func hasUnknownField(err *yaml.TypeError) bool {
	for _, msg := range err.Errors {
		if strings.Contains(msg, "not found in type") {
			return true
		}
	}
	return false
}

// Load reads the file at path and parses it. See Parse.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read configuration file: %w", err)
	}
	return Parse(data)
}
