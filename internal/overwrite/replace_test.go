package overwrite

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kites262/piperouter/internal/config"
)

func testPlan(rules ...config.OverwriteRule) *Plan {
	names := make([]string, len(rules))
	for i := range rules {
		rules[i].Name = string(rune('a' + i))
		rules[i].Enabled = true
		names[i] = rules[i].Name
	}
	return Compile(config.OverwriteConfig{Enabled: true, Rules: rules}, names)
}

func TestReplacePreservesOtherBytesAndOriginalMatches(t *testing.T) {
	p := testPlan(
		config.OverwriteRule{Field: "model", From: "a", To: "b"},
		config.OverwriteRule{Field: "model", From: "b", To: "c"},
		config.OverwriteRule{Field: "x.y", From: "", To: "quoted\"\n"},
	)
	body := []byte(" {\n \"messages\": [{\"model\":\"a\",\"n\":123456789012345678901234567890}], \"model\" : \"\\u0061\", \"x.y\":\"\" }")
	want := " {\n \"messages\": [{\"model\":\"a\",\"n\":123456789012345678901234567890}], \"model\" : \"b\", \"x.y\":\"quoted\\\"\\n\" }"
	got, err := p.Replace(body)
	if err != nil || string(got) != want {
		t.Fatalf("Replace = %s, %v; want %s", got, err, want)
	}
}

func TestReplaceRejectsInvalidOrAmbiguousInput(t *testing.T) {
	p := testPlan(config.OverwriteRule{Field: "model", From: "a", To: "b"})
	for _, body := range []string{"", "{", `{"model":"a",}`, `{"model":"a"} null`, `{"model":"a","\u006dodel":"a"}`, "{\"x\":\"\xff\"}"} {
		if _, err := p.Replace([]byte(body)); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("Replace(%q) err = %v", body, err)
		}
	}
	for _, body := range []string{"null", "[]", `"a"`, "0", "true"} {
		if _, err := p.Replace([]byte(body)); !errors.Is(err, ErrNotObject) {
			t.Errorf("Replace(%q) err = %v", body, err)
		}
	}
}

func TestReplaceUnchangedSharesInput(t *testing.T) {
	p := testPlan(config.OverwriteRule{Field: "model", From: "a", To: "b"})
	for _, body := range []string{`{}`, `{"model":"c"}`, `{"nested":{"model":"a"}}`, `{"model":null}`, `{"model":["a"]}`, `{"model":3}`, `{"model":true}`, `{"value":"bracket } comma , escaped \\\""}`} {
		input := []byte(body)
		got, err := p.Replace(input)
		if err != nil || !bytes.Equal(got, input) || &got[0] != &input[0] {
			t.Errorf("Replace(%s) = %s, %v; want unchanged slice", input, got, err)
		}
	}
}

func TestCompileDisabled(t *testing.T) {
	rules := []config.OverwriteRule{{Name: "a", Field: "model", From: "a", To: "b", Enabled: true}}
	for _, tc := range []struct {
		settings config.OverwriteConfig
		names    []string
	}{
		{config.OverwriteConfig{Rules: rules}, []string{"a"}},
		{config.OverwriteConfig{Enabled: true, Rules: rules}, nil},
		{config.OverwriteConfig{Enabled: true}, []string{"missing"}},
		{config.OverwriteConfig{Enabled: true, Rules: []config.OverwriteRule{{Name: "a"}}}, []string{"a"}},
	} {
		if p := Compile(tc.settings, tc.names); p != nil {
			t.Fatal("expected nil plan")
		}
	}
}

func FuzzReplace(f *testing.F) {
	for _, body := range []string{`{}`, `{"model":"a"}`, `{"a":[[{},"\\\"",1.3e-2]]}`, `{"model":"a", "nested":{"a":true}}`, `{"messages":[false,{"model":"a"},3,null],"values":[[1,2],[3,"a"]]}`} {
		f.Add([]byte(body))
	}
	p := testPlan(
		config.OverwriteRule{Path: ".model", From: "a", To: "b"},
		config.OverwriteRule{Path: ".messages[1].model", From: "a", To: "b"},
		config.OverwriteRule{Path: ".values[1][1]", From: "a", To: "b"},
		config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".model", Value: "a"}, Action: config.OverwriteAction{Path: ".values[0][0]", Value: "cross"}},
	)
	f.Fuzz(func(t *testing.T, body []byte) {
		out, err := p.Replace(body)
		if err == nil && !json.Valid(out) {
			t.Fatalf("produced invalid JSON: %q", out)
		}
	})
}

func BenchmarkReplace(b *testing.B) {
	p := testPlan(config.OverwriteRule{Field: "model", From: "a", To: "b"})
	for _, size := range []int{4 << 10, 1 << 20, 8 << 20} {
		body := []byte(`{"messages":[{"content":"` + strings.Repeat("x", size-64) + `"}],"model":"a"}`)
		unmatched := bytes.Replace(body, []byte(`"model":"a"`), []byte(`"model":"c"`), 1)
		for _, scenario := range []struct {
			name string
			plan *Plan
			body []byte
		}{
			{"disabled", nil, body},
			{"unmatched", p, unmatched},
			{"matched", p, body},
		} {
			b.Run(benchmarkName(size)+"/"+scenario.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(scenario.body)))
				for b.Loop() {
					_, _ = scenario.plan.Replace(scenario.body)
				}
			})
		}
	}
}

func benchmarkName(n int) string {
	if n >= 1<<20 {
		return string(rune('0'+n/(1<<20))) + "MiB"
	}
	return "4KiB"
}

func TestReplaceBoundsInputAndOutput(t *testing.T) {
	p := testPlan(config.OverwriteRule{Field: "model", From: "a", To: strings.Repeat("b", MaxBodyBytes)})
	if _, err := p.Replace([]byte(`{"model":"a"}`)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized output: %v", err)
	}
	if _, err := p.Replace(bytes.Repeat([]byte(" "), MaxBodyBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized input: %v", err)
	}
}

func TestReplaceJSONPaths(t *testing.T) {
	p := testPlan(
		config.OverwriteRule{Path: ".model", From: "a", To: "b"},
		config.OverwriteRule{Path: ".model", From: "b", To: "c"},
		config.OverwriteRule{Path: ".reasoning.effort", From: "low", To: "high"},
		config.OverwriteRule{Path: ".messages[1].model", From: "a", To: "nested"},
		config.OverwriteRule{Path: `.["a.b"]["中文"]`, From: "原值", To: "新值"},
		config.OverwriteRule{Path: ".values[1]", From: "a", To: "array"},
	)
	body := []byte(` {"model":"a", "reasoning" : {"effort":"low"}, "messages":[{"model":"a"},{"model":"a"}],"a.b":{"中文":"原值"},"values":[1,"a",true,null],"large":123456789012345678901234567890} `)
	want := ` {"model":"b", "reasoning" : {"effort":"high"}, "messages":[{"model":"a"},{"model":"nested"}],"a.b":{"中文":"新值"},"values":[1,"array",true,null],"large":123456789012345678901234567890} `
	got, err := p.Replace(body)
	if err != nil || string(got) != want {
		t.Fatalf("Replace = %s, %v; want %s", got, err, want)
	}
}

func TestReplacePathsMissingAndTypeMismatch(t *testing.T) {
	p := testPlan(config.OverwriteRule{Path: ".a[1].b", From: "x", To: "y"})
	for _, raw := range []string{`{}`, `{"a":[]}`, `{"a":[{"b":"x"}]}`, `{"a":"x"}`, `{"a":[1,"x"]}`, `{"a":[1,{"b":true}]}`, `{"a":[false,{"b":null}]}`} {
		body := []byte(raw)
		got, err := p.Replace(body)
		if err != nil || &got[0] != &body[0] {
			t.Fatalf("Replace(%s) changed or failed: %s, %v", body, got, err)
		}
	}
}

func TestReplaceRejectsDuplicateKeysInVisitedObjects(t *testing.T) {
	p := testPlan(config.OverwriteRule{Path: ".a[0].b", From: "x", To: "y"})
	for _, body := range []string{`{"a":[{"b":"x","\u0062":"x"}]}`, `{"a":[],"a":[]}`} {
		if _, err := p.Replace([]byte(body)); !errors.Is(err, ErrInvalidJSON) {
			t.Fatalf("Replace(%s) err = %v", body, err)
		}
	}
	// An unrelated nested object is not decoded to inspect its members.
	if _, err := p.Replace([]byte(`{"a":[],"unused":{"b":1,"b":2}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestCompileIgnoresUnsupportedPhase(t *testing.T) {
	for _, rule := range []config.OverwriteRule{
		{Path: ".model", Phase: "res", From: "a", To: "b"},
		{Condition: config.OverwriteCondition{Phase: "res", Path: ".model", Value: "a"}, Action: config.OverwriteAction{Phase: "req", Path: ".model", Value: "b"}},
		{Condition: config.OverwriteCondition{Phase: "req", Path: ".model", Value: "a"}, Action: config.OverwriteAction{Phase: "res", Path: ".model", Value: "b"}},
	} {
		if p := testPlan(rule); p != nil {
			t.Fatal("response phase must not execute on requests")
		}
	}
}

func TestConditionAndActionUseIndependentPathsAndOriginalValues(t *testing.T) {
	p := testPlan(
		config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".model", Value: "a"}, Action: config.OverwriteAction{Path: ".deployment", Value: "first"}},
		config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".deployment", Value: "old"}, Action: config.OverwriteAction{Path: ".model", Value: "b"}},
		config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".model", Value: "b"}, Action: config.OverwriteAction{Path: ".other", Value: "wrong"}},
		config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".other", Value: "c"}, Action: config.OverwriteAction{Path: ".deployment", Value: "last"}},
	)
	got, err := p.Replace([]byte(`{"deployment":"old", "model":"a", "other":"c"}`))
	if want := `{"deployment":"last", "model":"b", "other":"c"}`; err != nil || string(got) != want {
		t.Fatalf("Replace = %s, %v; want %s", got, err, want)
	}
}

func TestLaterActionCanRestoreOriginalValue(t *testing.T) {
	if p := testPlan(config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".model", Value: "a"}, Action: config.OverwriteAction{Path: ".model", Value: "a"}}); p != nil {
		t.Fatal("a group containing only identity rules should keep the fast path")
	}
	p := testPlan(
		config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".other", Value: "c"}, Action: config.OverwriteAction{Path: ".model", Value: "b"}},
		config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".model", Value: "a"}, Action: config.OverwriteAction{Path: ".model", Value: "a"}},
	)
	body := []byte(`{"model":"\u0061","other":"c"}`)
	got, err := p.Replace(body)
	if err != nil || &got[0] != &body[0] {
		t.Fatalf("Replace should preserve original bytes: %s, %v", got, err)
	}
}

func TestActionRequiresExistingString(t *testing.T) {
	p := testPlan(config.OverwriteRule{Condition: config.OverwriteCondition{Path: ".model", Value: "a"}, Action: config.OverwriteAction{Path: ".target[0].value", Value: "new"}})
	for _, raw := range []string{
		`{"model":"a"}`, `{"model":"a","target":[]}`, `{"model":"a","target":[{}]}`,
		`{"model":"a","target":[{"value":null}]}`, `{"model":"a","target":[{"value":1}]}`,
		`{"model":true,"target":[{"value":"old"}]}`,
	} {
		body := []byte(raw)
		got, err := p.Replace(body)
		if err != nil || &got[0] != &body[0] {
			t.Fatalf("Replace(%s) changed or failed: %s, %v", body, got, err)
		}
	}
	got, err := p.Replace([]byte(`{"model":"a","target":[{"value":"old"}]}`))
	if err != nil || string(got) != `{"model":"a","target":[{"value":"new"}]}` {
		t.Fatalf("Replace did not update existing string: %s, %v", got, err)
	}
}
