package secretmanager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ConsoleSecrets returns one row per secret, sorted by name.
func (s *Service) ConsoleSecrets() ([]map[string]any, error) {
	all, err := s.store.List(nsSecrets, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var sec secretResource
		if json.Unmarshal(v, &sec) != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":       sec.Name,
			"id":         lastSegment(sec.Name),
			"createTime": sec.CreateTime,
			"labels":     sec.Labels,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleVersions lists versions for a secret. `secret` accepts either a
// bare id or the fully qualified name; the qualified form is used for
// the store-key prefix.
func (s *Service) ConsoleVersions(secret string) ([]map[string]any, error) {
	prefix := secret + "/versions/"
	all, err := s.store.List(nsVersions, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for k, v := range all {
		var ver versionResource
		if json.Unmarshal(v, &ver) != nil {
			continue
		}
		num, _ := strconv.Atoi(strings.TrimPrefix(k, prefix))
		out = append(out, map[string]any{
			"name":       ver.Name,
			"version":    num,
			"state":      ver.State,
			"createTime": ver.CreateTime,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["version"].(int)
		b, _ := out[j]["version"].(int)
		return a > b
	})
	return out, nil
}

// ConsoleVersionPayload returns the decoded payload for a given secret
// version, identified by the secret's full name and the version number
// (or "latest" to pick the highest).
func (s *Service) ConsoleVersionPayload(secret, version string) (string, error) {
	if version == "latest" {
		prefix := secret + "/versions/"
		all, _ := s.store.List(nsVersions, prefix)
		highest := 0
		for k := range all {
			n, _ := strconv.Atoi(strings.TrimPrefix(k, prefix))
			if n > highest {
				highest = n
			}
		}
		if highest == 0 {
			return "", errors.New("no versions")
		}
		version = strconv.Itoa(highest)
	}
	full := fmt.Sprintf("%s/versions/%s", secret, version)
	data, err := s.store.Get(nsVersions, full)
	if err != nil {
		return "", errors.New("version not found")
	}
	var stored struct {
		versionResource
		PayloadB64 string `json:"payload_b64"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(stored.PayloadB64)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}
