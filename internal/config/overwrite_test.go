package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func overwriteConfig() *Config {
	c := baseConfig()
	c.Overwrite = OverwriteConfig{Enabled: true, Rules: []OverwriteRule{
		{Name: "model-alias", Enabled: true, Condition: OverwriteCondition{Phase: "req", Path: ".model", Op: "eq", Value: "gpt-6.1-sol"}, Action: OverwriteAction{Phase: "req", Path: ".model", Op: "set", Value: "gpt-6.1-sol-1"}},
	}}
	c.Routes[0].Proxy.Overwrites = []string{"model-alias"}
	return c
}

func TestOverwriteValidation(t *testing.T) {
	if err := Validate(overwriteConfig(), ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"invalid name", func(c *Config) { c.Overwrite.Rules[0].Name = "bad name" }, "name must match"},
		{"empty path", func(c *Config) { c.Overwrite.Rules[0].Condition.Path = "" }, "path must start"},
		{"invalid path", func(c *Config) { c.Overwrite.Rules[0].Condition.Path = "\xff" }, "UTF-8"},
		{"unsupported condition phase", func(c *Config) { c.Overwrite.Rules[0].Condition.Phase = "res" }, "condition phase"},
		{"unsupported action phase", func(c *Config) { c.Overwrite.Rules[0].Action.Phase = "res" }, "action phase"},
		{"conflicting legacy phase", func(c *Config) { c.Overwrite.Rules[0].Phase = "res" }, "legacy phase conflicts"},
		{"ambiguous legacy field", func(c *Config) { c.Overwrite.Rules[0].Field = "other" }, "different values"},
		{"invalid value", func(c *Config) { c.Overwrite.Rules[0].Action.Value = "\xff" }, "UTF-8"},
		{"unsupported condition op", func(c *Config) { c.Overwrite.Rules[0].Condition.Op = "gt" }, "must be eq"},
		{"unsupported action op", func(c *Config) { c.Overwrite.Rules[0].Action.Op = "remove" }, "must be set"},
		{"duplicate rule", func(c *Config) { c.Overwrite.Rules = append(c.Overwrite.Rules, c.Overwrite.Rules[0]) }, "duplicate overwrite rule name"},
		{"unknown reference", func(c *Config) { c.Routes[0].Proxy.Overwrites = []string{"missing"} }, "unknown overwrite rule"},
		{"duplicate binding", func(c *Config) { c.Routes[0].Proxy.Overwrites = []string{"model-alias", "model-alias"} }, "duplicate overwrite binding"},
		{"conflicting enabled rules", func(c *Config) {
			r := c.Overwrite.Rules[0]
			r.Name = "second"
			r.Action.Value = "different"
			c.Overwrite.Rules = append(c.Overwrite.Rules, r)
			c.Routes[0].Proxy.Overwrites = append(c.Routes[0].Proxy.Overwrites, r.Name)
		}, "duplicate path/from conditions"},
		{"equivalent paths conflict", func(c *Config) {
			r := c.Overwrite.Rules[0]
			r.Name = "second"
			r.Condition.Path = `.["model"]`
			r.Action.Path = `.["model"]`
			c.Overwrite.Rules = append(c.Overwrite.Rules, r)
			c.Routes[0].Proxy.Overwrites = append(c.Routes[0].Proxy.Overwrites, r.Name)
		}, "duplicate path/from conditions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := overwriteConfig()
			tc.mutate(c)
			if err := Validate(c, ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v; want %q", err, tc.want)
			}
		})
	}
	c := overwriteConfig()
	r := c.Overwrite.Rules[0]
	r.Name = "disabled"
	r.Enabled = false
	c.Overwrite.Rules = append(c.Overwrite.Rules, r)
	c.Routes[0].Proxy.Overwrites = append(c.Routes[0].Proxy.Overwrites, r.Name)
	if err := Validate(c, ""); err != nil {
		t.Fatalf("disabled duplicate condition: %v", err)
	}
	c.Overwrite.Rules[0].Condition.Path = `.["a.b/键"]`
	c.Overwrite.Rules[0].Action.Path = `.["a.b/键"]`
	c.Overwrite.Rules[0].Condition.Value = ""
	c.Overwrite.Rules[0].Action.Value = ""
	if err := Validate(c, ""); err != nil {
		t.Fatalf("literal key and empty values: %v", err)
	}
}

func TestOverwriteCloneAndRoundTrip(t *testing.T) {
	c := overwriteConfig()
	clone := c.Clone()
	clone.Overwrite.Rules[0].Action.Value = "changed"
	clone.Routes[0].Proxy.Overwrites[0] = "changed"
	if c.Overwrite.Rules[0].Action.Value != "gpt-6.1-sol-1" || c.Routes[0].Proxy.Overwrites[0] != "model-alias" {
		t.Fatal("clone shares rule or binding storage")
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Overwrite.Rules[0] != c.Overwrite.Rules[0] || decoded.Routes[0].Proxy.Overwrites[0] != "model-alias" {
		t.Fatal("JSON round-trip lost overwrite fields")
	}
}

func TestParseOverwriteBackwardCompatibleAndStrict(t *testing.T) {
	legacy, err := Parse([]byte("version: v0.3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Overwrite.Enabled || len(legacy.Overwrite.Rules) != 0 {
		t.Fatal("overwrite must default off")
	}
	valid := `version: v0.3
overwrite:
  enabled: true
  rules:
    - name: alias
      enabled: true
      field: model
      from: a
      to: b
routes:
  - name: api
    prefix: /api
    options:
      target: https://example.com
      overwrites: [alias]
`
	c, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(c, ""); err != nil {
		t.Fatal(err)
	}
	if r := c.Overwrite.Rules[0]; r.Field != "" || r.Path != "" || r.Phase != "" || !r.Enabled || r.Condition != (OverwriteCondition{Phase: "req", Path: ".model", Op: "eq", Value: "a"}) || r.Action != (OverwriteAction{Phase: "req", Path: ".model", Op: "set", Value: "b"}) {
		t.Fatalf("legacy rule lost behavior during normalization: %+v", r)
	}
	for _, malformed := range []string{
		strings.Replace(valid, "      field: model", "      fields: model", 1),
		`version: v0.3
routes:
  - name: file
    type: static
    prefix: /file
    options:
      file: /tmp/file
      overwrites: [alias]
`,
	} {
		if _, err := Parse([]byte(malformed)); err == nil {
			t.Fatal("accepted unknown overwrite field or static binding")
		}
	}
}

func TestOverwriteCanonicalAndLegacyNormalization(t *testing.T) {
	for _, rule := range []string{
		"      field: model\n      from: a\n      to: b\n",
		"      phase: req\n      field: model\n      from: a\n      to: b\n",
		"      path: .model\n      from: a\n      to: b\n",
		"      condition:\n        path: .model\n        value: a\n      action:\n        path: .model\n        value: b\n",
		"      phase: req\n      condition:\n        path: .model\n        value: a\n      action:\n        path: .model\n        value: b\n",
		"      condition:\n        phase: req\n        path: .model\n        value: a\n      action:\n        phase: req\n        path: .model\n        value: b\n",
	} {
		c, err := Parse([]byte("version: v0.3\noverwrite:\n  enabled: true\n  rules:\n    - name: alias\n      enabled: true\n" + rule))
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(c, ""); err != nil {
			t.Fatal(err)
		}
		got := c.Overwrite.Rules[0]
		want := OverwriteRule{Name: "alias", Enabled: true, Condition: OverwriteCondition{Phase: "req", Path: ".model", Op: "eq", Value: "a"}, Action: OverwriteAction{Phase: "req", Path: ".model", Op: "set", Value: "b"}}
		if got != want {
			t.Fatalf("Normalize = %+v; want %+v", got, want)
		}
		c.Normalize()
		if c.Overwrite.Rules[0] != want {
			t.Fatal("Normalize is not idempotent")
		}
		data, err := json.Marshal(c.Overwrite.Rules[0])
		if err != nil || strings.Contains(string(data), `"field":`) || strings.Contains(string(data), `"from":`) || strings.Contains(string(data), `"to":`) {
			t.Fatalf("legacy fields remain in canonical JSON: %s, %v", data, err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if _, hasRootPhase := wire["phase"]; hasRootPhase {
			t.Fatal("canonical rule still emits a shared phase")
		}
	}
}

func TestOverwriteAllowsOneConditionWithDistinctActionPaths(t *testing.T) {
	c := overwriteConfig()
	r := c.Overwrite.Rules[0]
	r.Name = "other-action"
	r.Action.Path = ".deployment"
	c.Overwrite.Rules = append(c.Overwrite.Rules, r)
	c.Routes[0].Proxy.Overwrites = append(c.Routes[0].Proxy.Overwrites, r.Name)
	if err := Validate(c, ""); err != nil {
		t.Fatal(err)
	}
}
