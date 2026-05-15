package firestore

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/GuitarWag/gcp-local/internal/state"
)

// docNameToCollection extracts the parent-collection path from a stored
// document name of the form
// projects/{p}/databases/{db}/documents/{collection}/{doc}[/...].
// Returns the empty string if the path doesn't look like a document.
func docNameToCollection(name string) string {
	const marker = "/documents/"
	i := strings.Index(name, marker)
	if i < 0 {
		return ""
	}
	rest := name[i+len(marker):]
	// Walk back to the last collection segment (before final doc id).
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return ""
	}
	parts = parts[:len(parts)-1] // drop doc id
	return strings.Join(parts, "/")
}

// ConsoleCollections returns every distinct collection path that
// currently has at least one document, sorted lexicographically. Nested
// subcollections are returned with their full path (e.g. users/u1/posts).
func (s *Service) ConsoleCollections() ([]string, error) {
	all, err := s.store.List(nsDocs, "")
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for k := range all {
		coll := docNameToCollection(k)
		if coll == "" {
			continue
		}
		seen[coll] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// ConsoleDocuments returns one row per direct-child document of the
// given collection path. Each row is {id, name, updateTime} — the full
// payload is available via ConsoleDocument(name).
func (s *Service) ConsoleDocuments(collection string) ([]map[string]any, error) {
	if collection == "" {
		return nil, errors.New("collection required")
	}
	all, err := s.store.List(nsDocs, "")
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, raw := range all {
		var d storedDoc
		if json.Unmarshal(raw, &d) != nil {
			continue
		}
		if docNameToCollection(d.Name) != collection {
			continue
		}
		segs := strings.Split(d.Name, "/")
		id := segs[len(segs)-1]
		out = append(out, map[string]any{
			"id":         id,
			"name":       d.Name,
			"createTime": d.CreateTime,
			"updateTime": d.UpdateTime,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["id"].(string)
		b, _ := out[j]["id"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleDocument returns a single document by its full path, decoded
// into a flat map (the raw Firestore-API field shape is preserved so
// the UI can render type+value pairs without us guessing semantics).
func (s *Service) ConsoleDocument(name string) (map[string]any, error) {
	d, err := s.getDoc(name)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, errors.New("document not found")
		}
		return nil, err
	}
	fields := map[string]any{}
	for k, v := range d.Fields {
		var decoded any
		if json.Unmarshal(v, &decoded) == nil {
			fields[k] = decoded
		} else {
			fields[k] = string(v)
		}
	}
	return map[string]any{
		"name":       d.Name,
		"createTime": d.CreateTime,
		"updateTime": d.UpdateTime,
		"fields":     fields,
	}, nil
}
