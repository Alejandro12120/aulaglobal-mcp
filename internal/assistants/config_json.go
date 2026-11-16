package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// JSON editing shared by the assistants that keep their servers in a JSON file.

// strictJSON rejects duplicate keys as well as invalid JSON: encoding/json
// would otherwise drop one of the duplicates silently when the file is rewritten.
func strictJSON(b []byte) error {
	d := json.NewDecoder(strings.NewReader(string(b)))
	var value func() error
	value = func() error {
		tok, e := d.Token()
		if e != nil {
			return e
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := k.(string)
					if !ok || seen[s] {
						return errors.New("duplicate key")
					}
					seen[s] = true
					if e = value(); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e = value(); e != nil {
						return e
					}
				}
			default:
				return errors.New("invalid delimiter")
			}
			_, e = d.Token()
			return e
		}
		return nil
	}
	if e := value(); e != nil {
		return e
	}
	if d.More() {
		return errors.New("extra JSON")
	}
	return nil
}

// mcpServersConfig sets mcpServers.aulaglobal in a Claude Desktop style config
// and keeps every other key. exists reports whether the entry was already
// there; an existing entry is replaced only when replace is true, otherwise no
// bytes are returned.
func mcpServersConfig(old []byte, entry map[string]any, replace bool) ([]byte, bool, error) {
	root := map[string]json.RawMessage{}
	if len(old) > 0 {
		if e := strictJSON(old); e != nil {
			return nil, false, e
		}
		if e := json.Unmarshal(old, &root); e != nil {
			return nil, false, e
		}
		if root == nil {
			return nil, false, errors.New("object required")
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := root["mcpServers"]; ok {
		if e := json.Unmarshal(raw, &servers); e != nil {
			return nil, false, e
		}
		if servers == nil {
			return nil, false, errors.New("object required")
		}
	}
	_, exists := servers[aulaglobalKey]
	if exists && !replace {
		return nil, true, nil
	}
	b, e := json.Marshal(entry)
	if e != nil {
		return nil, exists, e
	}
	servers[aulaglobalKey] = b
	root["mcpServers"], e = json.Marshal(servers)
	if e != nil {
		return nil, exists, e
	}
	out, e := json.MarshalIndent(root, "", "  ")
	return out, exists, e
}

func mcpServersEntry(old []byte) (map[string]any, bool, error) {
	return jsonServerEntry(old, []string{"mcpServers"})
}

func mcpServersRemove(old []byte) ([]byte, bool, error) {
	return jsonRemoveServer(old, []string{"mcpServers"})
}

// jsonServerEntry returns the aulaglobal object under path, if any.
func jsonServerEntry(old []byte, path []string) (map[string]any, bool, error) {
	if len(bytes.TrimSpace(old)) == 0 {
		return nil, false, nil
	}
	if e := strictJSON(old); e != nil {
		return nil, false, e
	}
	var cur any
	if e := json.Unmarshal(old, &cur); e != nil {
		return nil, false, e
	}
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, errors.New("object required")
		}
		if cur, ok = m[p]; !ok {
			return nil, false, nil
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, false, errors.New("object required")
	}
	v, ok := m[aulaglobalKey]
	if !ok {
		return nil, false, nil
	}
	entry, ok := v.(map[string]any)
	if !ok {
		return nil, true, errors.New("unexpected entry")
	}
	return entry, true, nil
}

// jsonRemoveServer removes the aulaglobal member under path. found is false
// when the entry was not there and nothing needs writing.
func jsonRemoveServer(old []byte, path []string) ([]byte, bool, error) {
	if e := strictJSON(old); e != nil {
		return nil, false, e
	}
	s, e, found, err := jsonMemberSpan(old, path, aulaglobalKey)
	if err != nil || !found {
		return nil, false, err
	}
	out := append(append([]byte{}, old[:s]...), old[e:]...)
	if !json.Valid(out) {
		return nil, true, errors.New("unsafe removal")
	}
	if _, _, again, err := jsonMemberSpan(out, path, aulaglobalKey); err != nil || again {
		return nil, true, errors.New("entry still present")
	}
	return out, true, nil
}

func jsonSkipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// jsonMemberSpan finds member key inside the object at path and returns the
// byte range whose removal leaves valid JSON with every other byte untouched
// (same key order, indentation and number formatting). The document must have
// passed strictJSON, so keys are unique.
func jsonMemberSpan(data []byte, path []string, key string) (start, end int, found bool, err error) {
	d := json.NewDecoder(bytes.NewReader(data))
	if tok, e := d.Token(); e != nil || tok != json.Delim('{') {
		return 0, 0, false, errors.New("object required")
	}
	for len(path) > 0 {
		match := false
		for d.More() {
			kt, e := d.Token()
			if e != nil {
				return 0, 0, false, e
			}
			if kt.(string) == path[0] {
				vt, e := d.Token()
				if e != nil {
					return 0, 0, false, e
				}
				if vt != json.Delim('{') {
					return 0, 0, false, fmt.Errorf("%s must be an object", path[0])
				}
				match = true
				break
			}
			var skip json.RawMessage
			if e = d.Decode(&skip); e != nil {
				return 0, 0, false, e
			}
		}
		if !match {
			return 0, 0, false, nil
		}
		path = path[1:]
	}
	prevEnd := int(d.InputOffset())
	first := true
	for d.More() {
		kt, e := d.Token()
		if e != nil {
			return 0, 0, false, e
		}
		var raw json.RawMessage
		if e = d.Decode(&raw); e != nil {
			return 0, 0, false, e
		}
		valEnd := int(d.InputOffset())
		if kt.(string) != key {
			prevEnd, first = valEnd, false
			continue
		}
		switch {
		case !first:
			return prevEnd, valEnd, true, nil
		default:
			i := jsonSkipSpace(data, valEnd)
			if i < len(data) && data[i] == ',' {
				return jsonSkipSpace(data, prevEnd), jsonSkipSpace(data, i+1), true, nil
			}
			return prevEnd, i, true, nil // only member: leave "{}"
		}
	}
	return 0, 0, false, nil
}
