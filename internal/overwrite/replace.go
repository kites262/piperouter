// Package overwrite compiles immutable JSON path string replacements.
// It preserves untouched bytes, including large nested values and number precision.
package overwrite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/kites262/piperouter/internal/config"
)

// MaxBodyBytes bounds request buffering when a route has active replacements.
const MaxBodyBytes = 8 << 20

var (
	ErrInvalidJSON = errors.New("overwrite: invalid JSON object")
	ErrNotObject   = errors.New("overwrite: JSON body must be an object")
	ErrTooLarge    = errors.New("overwrite: JSON body exceeds 8 MiB limit")
)

// Plan is immutable and safe for concurrent requests.
type Plan struct {
	root       pathNode
	valueCount int
	rules      []compiledRule
}

type pathNode struct {
	keys       map[string]*pathNode
	indexes    map[int]*pathNode
	capture    bool
	valueIndex int
}

type compiledRule struct {
	condition   int
	action      int
	match       string
	value       string
	replacement []byte
}

type capturedValue struct {
	start, end       int
	value            string
	exists           bool
	replacement      []byte
	replacementValue string
}

type change struct {
	start, end int
	value      []byte
}

// Compile prepares a validated route's rules once per configuration snapshot.
// A nil plan is the fast path for disabled overwrite or no active bindings.
func Compile(settings config.OverwriteConfig, names []string) *Plan {
	if !settings.Enabled || len(names) == 0 {
		return nil
	}
	byName := make(map[string]config.OverwriteRule, len(settings.Rules))
	for _, rule := range settings.Rules {
		byName[rule.Name] = rule
	}
	p := &Plan{}
	hasChange := false
	for _, name := range names {
		rule, exists := byName[name]
		if !exists || !rule.Enabled {
			continue
		}
		condition, action := rule.EffectiveCondition(), rule.EffectiveAction()
		if condition.Phase != "req" || action.Phase != "req" {
			continue
		}
		conditionPath, conditionErr := config.ParseOverwritePath(condition.Path)
		actionPath, actionErr := config.ParseOverwritePath(action.Path)
		if conditionErr != nil || actionErr != nil || condition.Op != "eq" || action.Op != "set" {
			continue // Build normally receives validated configuration.
		}
		conditionIndex, actionIndex := p.addPath(conditionPath), p.addPath(actionPath)
		if conditionIndex != actionIndex || condition.Value != action.Value {
			hasChange = true
		}
		encoded, _ := json.Marshal(action.Value) // Config validation guarantees UTF-8.
		p.rules = append(p.rules, compiledRule{condition: conditionIndex, action: actionIndex, match: condition.Value, value: action.Value, replacement: encoded})
	}
	if !hasChange {
		return nil
	}
	return p
}

func (p *Plan) addPath(tokens []config.OverwritePathToken) int {
	node := &p.root
	for _, token := range tokens {
		if token.IsIndex {
			if node.indexes == nil {
				node.indexes = make(map[int]*pathNode)
			}
			if node.indexes[token.Index] == nil {
				node.indexes[token.Index] = &pathNode{}
			}
			node = node.indexes[token.Index]
		} else {
			if node.keys == nil {
				node.keys = make(map[string]*pathNode)
			}
			if node.keys[token.Key] == nil {
				node.keys[token.Key] = &pathNode{}
			}
			node = node.keys[token.Key]
		}
	}
	if !node.capture {
		node.capture = true
		node.valueIndex = p.valueCount
		p.valueCount++
	}
	return node.valueIndex
}

// Replace validates a complete JSON object and changes strings at matching paths.
// All comparisons use original values, never earlier outputs. An unchanged result
// shares the input slice. Duplicate keys in objects visited by a path are rejected,
// including differently escaped spellings of a key.
func (p *Plan) Replace(body []byte) ([]byte, error) {
	if p == nil {
		return body, nil
	}
	if len(body) > MaxBodyBytes {
		return nil, ErrTooLarge
	}
	if !utf8.Valid(body) || !json.Valid(body) {
		return nil, ErrInvalidJSON
	}
	i := skipSpace(body, 0)
	if body[i] != '{' {
		return nil, ErrNotObject
	}
	values := make([]capturedValue, p.valueCount)
	if _, err := visit(body, i, &p.root, values); err != nil {
		return nil, err
	}
	// Every condition reads the captured original value. Later bound rules may
	// replace an earlier write to the same action path, without chaining matches.
	for _, rule := range p.rules {
		condition := &values[rule.condition]
		action := &values[rule.action]
		if condition.exists && condition.value == rule.match && action.exists {
			action.replacement = rule.replacement
			action.replacementValue = rule.value
		}
	}
	var changes []change
	for _, value := range values {
		if value.replacement != nil && value.value != value.replacementValue {
			changes = append(changes, change{value.start, value.end, value.replacement})
		}
	}
	if len(changes) == 0 {
		return body, nil
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].start < changes[j].start })
	outputBytes := int64(len(body))
	for _, c := range changes {
		outputBytes += int64(len(c.value)) - int64(c.end-c.start)
	}
	if outputBytes > MaxBodyBytes {
		return nil, ErrTooLarge
	}
	var out bytes.Buffer
	out.Grow(int(outputBytes))
	last := 0
	for _, c := range changes {
		out.Write(body[last:c.start])
		out.Write(c.value)
		last = c.end
	}
	out.Write(body[last:])
	return out.Bytes(), nil
}

// visit only descends through configured paths. Large unrelated messages and
// tool values are scanned without decoding or allocating their JSON structures.
func visit(body []byte, start int, node *pathNode, values []capturedValue) (int, error) {
	switch body[start] {
	case '"':
		end := stringEnd(body, start)
		if node.capture {
			var original string
			_ = json.Unmarshal(body[start:end], &original)
			values[node.valueIndex] = capturedValue{start: start, end: end, value: original, exists: true}
		}
		return end, nil
	case '{':
		seen := make(map[string]struct{})
		i := skipSpace(body, start+1)
		for body[i] != '}' {
			keyEnd := stringEnd(body, i)
			var key string
			_ = json.Unmarshal(body[i:keyEnd], &key)
			if _, exists := seen[key]; exists {
				return 0, fmt.Errorf("%w: duplicate key in accessed object", ErrInvalidJSON)
			}
			seen[key] = struct{}{}
			i = skipSpace(body, keyEnd)
			i = skipSpace(body, i+1) // colon, guaranteed by json.Valid
			if child := node.keys[key]; child != nil {
				var err error
				i, err = visit(body, i, child, values)
				if err != nil {
					return 0, err
				}
			} else {
				i = valueEnd(body, i)
			}
			i = skipSpace(body, i)
			if body[i] == ',' {
				i = skipSpace(body, i+1)
			}
		}
		return i + 1, nil
	case '[':
		if len(node.indexes) == 0 {
			return valueEnd(body, start), nil
		}
		i := skipSpace(body, start+1)
		for index := 0; body[i] != ']'; index++ {
			if child := node.indexes[index]; child != nil {
				var err error
				i, err = visit(body, i, child, values)
				if err != nil {
					return 0, err
				}
			} else {
				i = valueEnd(body, i)
			}
			i = skipSpace(body, i)
			if body[i] == ',' {
				i = skipSpace(body, i+1)
			}
		}
		return i + 1, nil
	default:
		return valueEnd(body, start), nil
	}
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	return i
}

// stringEnd and valueEnd only scan JSON already checked by json.Valid.
func stringEnd(b []byte, i int) int {
	i++
	for {
		switch b[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
}

func valueEnd(b []byte, i int) int {
	if b[i] == '"' {
		return stringEnd(b, i)
	}
	if b[i] == '[' || b[i] == '{' {
		depth := 0
		for {
			switch b[i] {
			case '"':
				i = stringEnd(b, i)
				continue
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
	}
	for i < len(b) && b[i] != ',' && b[i] != '}' && b[i] != ']' && b[i] != ' ' && b[i] != '\n' && b[i] != '\r' && b[i] != '\t' {
		i++
	}
	return i
}
