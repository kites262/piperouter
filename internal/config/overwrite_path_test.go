package config

import (
	"reflect"
	"testing"
)

func TestParseOverwritePath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []OverwritePathToken
	}{
		{".", nil},
		{".model", []OverwritePathToken{{Key: "model"}}},
		{".reasoning.effort", []OverwritePathToken{{Key: "reasoning"}, {Key: "effort"}}},
		{".messages[0].model", []OverwritePathToken{{Key: "messages"}, {Index: 0, IsIndex: true}, {Key: "model"}}},
		{`.["a.b"]["引号\"\\"][1]`, []OverwritePathToken{{Key: "a.b"}, {Key: "引号\"\\"}, {Index: 1, IsIndex: true}}},
		{".中文_键.$value", []OverwritePathToken{{Key: "中文_键"}, {Key: "$value"}}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := ParseOverwritePath(tc.path)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parse = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
	for _, path := range []string{"", "model", "$.model", "..model", ".model.", ".model[", ".model[-1]", ".model[*]", ".model[?(@.a)]", ".['key']", ".[0:2]", ".2bad", ".[9999999999999999999999]", `.["bad\x"]`, ".\xff"} {
		if _, err := ParseOverwritePath(path); err == nil {
			t.Errorf("accepted unsupported path %q", path)
		}
	}
}

func TestOverwriteNormalizeLegacyPaths(t *testing.T) {
	for _, key := range []string{"model", "a.b", "引号\"\\", "", "with space"} {
		r := OverwriteRule{Field: key, Enabled: true}
		r.normalize()
		if key == "" {
			continue
		}
		tokens, err := ParseOverwritePath(r.Condition.Path)
		if err != nil || len(tokens) != 1 || tokens[0].Key != key || r.Field != "" || r.Path != "" || r.Phase != "" || r.Condition.Phase != "req" || r.Action.Phase != "req" || r.Condition.Op != "eq" || r.Action.Op != "set" || r.Action.Path != r.Condition.Path || !r.Enabled {
			t.Fatalf("normalize %q = %+v, tokens=%+v, err=%v", key, r, tokens, err)
		}
	}
	r := OverwriteRule{Path: ".model", Field: "model"}
	r.normalize()
	if r.Field != "" {
		t.Fatal("equivalent legacy key was not cleared")
	}
	r = OverwriteRule{Path: ".other", Field: "model"}
	r.normalize()
	if r.Field != "model" {
		t.Fatal("ambiguous legacy key was silently dropped")
	}
	c := overwriteConfig()
	c.Overwrite.Rules[0] = r
	c.Overwrite.Rules[0].Name = "model-alias"
	if err := Validate(c, ""); err == nil {
		t.Fatal("ambiguous normalized rule was accepted")
	}
}
