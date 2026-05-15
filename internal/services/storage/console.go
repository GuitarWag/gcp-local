package storage

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/GuitarWag/gcp-local/internal/state"
)

// ConsoleBuckets returns a flat list of buckets ordered by name. The
// shape matches what the /console UI renders; full bucket metadata is
// still available via the standard REST surface.
func (s *Service) ConsoleBuckets() ([]map[string]any, error) {
	all, err := s.store.List(nsBuckets, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var b bucketResource
		if json.Unmarshal(v, &b) != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":     b.Name,
			"location": b.Location,
			"class":    b.StorageClass,
			"created":  b.TimeCreated,
			"updated":  b.Updated,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleObjects lists objects in a bucket. Returns the empty slice (not
// an error) if the bucket exists but is empty.
func (s *Service) ConsoleObjects(bucket string) ([]map[string]any, error) {
	if _, err := s.store.Get(nsBuckets, bucket); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, fmt.Errorf("bucket not found: %s", bucket)
		}
		return nil, err
	}
	all, err := s.store.List(objectNamespace(bucket), "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var o objectResource
		if json.Unmarshal(v, &o) != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":        o.Name,
			"size":        o.Size,
			"contentType": o.ContentType,
			"updated":     o.Updated,
			"md5":         o.MD5Hash,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleObjectPreview returns the first maxBytes of an object's body
// for display in the console. The isText flag is true when the bytes
// look like valid UTF-8 with no NULs; the UI uses it to decide between
// text and hex rendering.
func (s *Service) ConsoleObjectPreview(bucket, object string, maxBytes int) (string, bool, error) {
	if _, err := s.store.Get(nsBuckets, bucket); err != nil {
		return "", false, fmt.Errorf("bucket not found: %s", bucket)
	}
	body, err := s.store.Get(objectDataNamespace(bucket), object)
	if err != nil {
		return "", false, err
	}
	if maxBytes > 0 && len(body) > maxBytes {
		body = body[:maxBytes]
	}
	isText := utf8.Valid(body)
	for _, b := range body {
		if b == 0 {
			isText = false
			break
		}
	}
	if isText {
		return string(body), true, nil
	}
	return hex.Dump(body), false, nil
}

// ConsoleUpload writes a small object via the same path the XML PUT
// handler uses (storeObject). Caller is expected to enforce its own
// size limit before reaching here.
func (s *Service) ConsoleUpload(bucket, object, contentType string, data []byte) error {
	if object == "" {
		return errors.New("object name is required")
	}
	if _, err := s.store.Get(nsBuckets, bucket); err != nil {
		return fmt.Errorf("bucket not found: %s", bucket)
	}
	if contentType == "" {
		contentType = detectContentType(object, data)
	}
	s.storeObject(bucket, object, contentType, data)
	return nil
}
