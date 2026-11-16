package assistants

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
func strictJSON(data []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for decoder.More() {
					keyToken, err := decoder.Token()
					if err != nil {
						return err
					}
					name, ok := keyToken.(string)
					if !ok || seen[name] {
						return errors.New("duplicate key")
					}
					seen[name] = true
					if err = value(); err != nil {
						return err
					}
				}
			case '[':
				for decoder.More() {
					if err = value(); err != nil {
						return err
					}
				}
			default:
				return errors.New("invalid delimiter")
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := value(); err != nil {
		return err
	}
	if decoder.More() {
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
		if err := strictJSON(old); err != nil {
			return nil, false, err
		}
		if err := json.Unmarshal(old, &root); err != nil {
			return nil, false, err
		}
		if root == nil {
			return nil, false, errors.New("object required")
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := root["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, false, err
		}
		if servers == nil {
			return nil, false, errors.New("object required")
		}
	}
	_, exists := servers[aulaglobalKey]
	if exists && !replace {
		return nil, true, nil
	}
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return nil, exists, err
	}
	servers[aulaglobalKey] = entryJSON
	root["mcpServers"], err = json.Marshal(servers)
	if err != nil {
		return nil, exists, err
	}
	out, err := json.MarshalIndent(root, "", "  ")
	return out, exists, err
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
	if err := strictJSON(old); err != nil {
		return nil, false, err
	}
	var cur any
	if err := json.Unmarshal(old, &cur); err != nil {
		return nil, false, err
	}
	for _, key := range path {
		object, ok := cur.(map[string]any)
		if !ok {
			return nil, false, errors.New("object required")
		}
		if cur, ok = object[key]; !ok {
			return nil, false, nil
		}
	}
	object, ok := cur.(map[string]any)
	if !ok {
		return nil, false, errors.New("object required")
	}
	value, ok := object[aulaglobalKey]
	if !ok {
		return nil, false, nil
	}
	entry, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("unexpected entry")
	}
	return entry, true, nil
}

// jsonRemoveServer removes the aulaglobal member under path. found is false
// when the entry was not there and nothing needs writing.
func jsonRemoveServer(old []byte, path []string) ([]byte, bool, error) {
	if err := strictJSON(old); err != nil {
		return nil, false, err
	}
	start, end, found, err := jsonMemberSpan(old, path, aulaglobalKey)
	if err != nil || !found {
		return nil, false, err
	}
	out := append(append([]byte{}, old[:start]...), old[end:]...)
	if !json.Valid(out) {
		return nil, true, errors.New("unsafe removal")
	}
	if _, _, again, err := jsonMemberSpan(out, path, aulaglobalKey); err != nil || again {
		return nil, true, errors.New("entry still present")
	}
	return out, true, nil
}

func jsonSkipSpace(data []byte, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\n' || data[i] == '\r') {
		i++
	}
	return i
}

// jsonMemberSpan finds member key inside the object at path and returns the
// byte range whose removal leaves valid JSON with every other byte untouched
// (same key order, indentation and number formatting). The document must have
// passed strictJSON, so keys are unique.
func jsonMemberSpan(data []byte, path []string, key string) (start, end int, found bool, err error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return 0, 0, false, errors.New("object required")
	}
	for len(path) > 0 {
		match := false
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return 0, 0, false, err
			}
			if keyToken.(string) == path[0] {
				valueToken, err := decoder.Token()
				if err != nil {
					return 0, 0, false, err
				}
				if valueToken != json.Delim('{') {
					return 0, 0, false, fmt.Errorf("%s must be an object", path[0])
				}
				match = true
				break
			}
			var skip json.RawMessage
			if err = decoder.Decode(&skip); err != nil {
				return 0, 0, false, err
			}
		}
		if !match {
			return 0, 0, false, nil
		}
		path = path[1:]
	}
	prevEnd := int(decoder.InputOffset())
	first := true
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return 0, 0, false, err
		}
		var raw json.RawMessage
		if err = decoder.Decode(&raw); err != nil {
			return 0, 0, false, err
		}
		valEnd := int(decoder.InputOffset())
		if keyToken.(string) != key {
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
