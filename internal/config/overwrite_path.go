package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// OverwritePathToken is one object member or zero-based array index.
type OverwritePathToken struct {
	Key     string
	Index   int
	IsIndex bool
}

// ParseOverwritePath parses direct JSON access paths rooted at a leading dot.
// Dot members, JSON double-quoted bracket members, and array indexes are
// supported; wildcards, filters, recursive descent, and expressions are not.
func ParseOverwritePath(path string) ([]OverwritePathToken, error) {
	if !utf8.ValidString(path) {
		return nil, fmt.Errorf("path must be valid UTF-8")
	}
	if path == "" || path[0] != '.' {
		return nil, fmt.Errorf("path must start with '.' (the complete JSON body)")
	}
	var tokens []OverwritePathToken
	i := 1
	member := func() error {
		start := i
		for i < len(path) {
			r, n := utf8.DecodeRuneInString(path[i:])
			if !unicode.IsLetter(r) && r != '_' && r != '$' && !(i > start && unicode.IsDigit(r)) {
				break
			}
			i += n
		}
		if i == start {
			return fmt.Errorf("path requires a member name at byte %d", i)
		}
		tokens = append(tokens, OverwritePathToken{Key: path[start:i]})
		return nil
	}
	if i < len(path) && path[i] != '[' {
		if err := member(); err != nil {
			return nil, err
		}
	}
	for i < len(path) {
		switch path[i] {
		case '.':
			i++
			if err := member(); err != nil {
				return nil, err
			}
		case '[':
			i++
			if i < len(path) && path[i] == '"' {
				start := i
				i++
				for i < len(path) && path[i] != '"' {
					if path[i] == '\\' {
						i++
					}
					i++
				}
				if i >= len(path) {
					return nil, fmt.Errorf("path has an unterminated quoted member")
				}
				i++
				var key string
				if err := json.Unmarshal([]byte(path[start:i]), &key); err != nil {
					return nil, fmt.Errorf("path has an invalid quoted member: %w", err)
				}
				tokens = append(tokens, OverwritePathToken{Key: key})
			} else {
				start := i
				for i < len(path) && path[i] >= '0' && path[i] <= '9' {
					i++
				}
				if start == i {
					return nil, fmt.Errorf("path requires a quoted member or non-negative array index")
				}
				index, err := strconv.Atoi(path[start:i])
				if err != nil {
					return nil, fmt.Errorf("path array index is too large")
				}
				tokens = append(tokens, OverwritePathToken{Index: index, IsIndex: true})
			}
			if i >= len(path) || path[i] != ']' {
				return nil, fmt.Errorf("path requires a closing bracket")
			}
			i++
		default:
			return nil, fmt.Errorf("path has an unsupported character at byte %d", i)
		}
	}
	return tokens, nil
}

func (r OverwriteRule) legacyPath() string {
	if r.Path != "" {
		return r.Path
	}
	if r.Field == "" {
		return ""
	}
	return literalOverwritePath(r.Field)
}

// EffectiveCondition also handles old rules before Normalize is called.
func (r OverwriteRule) EffectiveCondition() OverwriteCondition {
	c := r.Condition
	if r.Condition == (OverwriteCondition{}) && r.Action == (OverwriteAction{}) {
		c = OverwriteCondition{Path: r.legacyPath(), Op: "eq", Value: r.From}
	}
	c.Phase = overwritePhase(c.Phase, r.Phase)
	if c.Op == "" {
		c.Op = "eq"
	}
	return c
}

func (r OverwriteRule) EffectiveAction() OverwriteAction {
	a := r.Action
	if r.Condition == (OverwriteCondition{}) && r.Action == (OverwriteAction{}) {
		a = OverwriteAction{Path: r.legacyPath(), Op: "set", Value: r.To}
	}
	a.Phase = overwritePhase(a.Phase, r.Phase)
	if a.Op == "" {
		a.Op = "set"
	}
	return a
}

func overwritePhase(phase, legacy string) string {
	if phase != "" {
		return phase
	}
	if legacy != "" {
		return legacy
	}
	return "req"
}

func (r *OverwriteRule) normalize() {
	// Compute both against the same original rule before filling defaults.
	condition, action := r.EffectiveCondition(), r.EffectiveAction()
	r.Condition, r.Action = condition, action
	if r.legacyPhaseMatches() {
		r.Phase = ""
	}
	if r.legacyMatches() {
		r.Path = ""
		r.Field = ""
		r.From = ""
		r.To = ""
	}
}

func (r OverwriteRule) legacyPhaseMatches() bool {
	return r.Phase == "" || (r.EffectiveCondition().Phase == r.Phase && r.EffectiveAction().Phase == r.Phase)
}

func (r OverwriteRule) hasLegacy() bool {
	return r.Path != "" || r.Field != "" || r.From != "" || r.To != ""
}

func (r OverwriteRule) legacyMatches() bool {
	if !r.hasLegacy() {
		return true
	}
	if !utf8.ValidString(r.Field) || !utf8.ValidString(r.From) || !utf8.ValidString(r.To) {
		return false
	}
	legacy := r.legacyPath()
	if r.Path != "" && r.Field != "" && !sameOverwritePath(r.Path, literalOverwritePath(r.Field)) {
		return false
	}
	c, a := r.EffectiveCondition(), r.EffectiveAction()
	return sameOverwritePath(c.Path, legacy) && sameOverwritePath(a.Path, legacy) &&
		c.Op == "eq" && a.Op == "set" && c.Value == r.From && a.Value == r.To
}

func sameOverwritePath(a, b string) bool {
	left, leftErr := ParseOverwritePath(a)
	right, rightErr := ParseOverwritePath(b)
	return leftErr == nil && rightErr == nil && canonicalOverwritePath(left) == canonicalOverwritePath(right)
}

func literalOverwritePath(key string) string {
	if tokens, err := ParseOverwritePath("." + key); err == nil && len(tokens) == 1 && !tokens[0].IsIndex && tokens[0].Key == key {
		return "." + key
	}
	encoded, _ := json.Marshal(key)
	return ".[" + string(encoded) + "]"
}

func canonicalOverwritePath(tokens []OverwritePathToken) string {
	var b strings.Builder
	for _, token := range tokens {
		if token.IsIndex {
			fmt.Fprintf(&b, "[%d]", token.Index)
		} else {
			encoded, _ := json.Marshal(token.Key)
			b.WriteByte('[')
			b.Write(encoded)
			b.WriteByte(']')
		}
	}
	return b.String()
}
