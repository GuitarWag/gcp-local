package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/GuitarWag/gcp-local/internal/state"
)

var generation uint64

func (s *Service) handleObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := s.store.Get(nsBuckets, bucket); err != nil {
		s.writeErr(w, http.StatusNotFound, "bucket not found")
		return
	}
	if r.Method != http.MethodGet {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	prefix := r.URL.Query().Get("prefix")
	items, err := s.store.List(objectNamespace(bucket), prefix)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := objectsList{Kind: "storage#objects", Items: []objectResource{}}
	for _, v := range items {
		var o objectResource
		if err := json.Unmarshal(v, &o); err != nil {
			continue
		}
		out.Items = append(out.Items, o)
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Service) handleObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	objName, err := url.PathUnescape(object)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid object name")
		return
	}
	if _, err := s.store.Get(nsBuckets, bucket); err != nil {
		s.writeErr(w, http.StatusNotFound, "bucket not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("alt") == "media" {
			s.downloadObject(w, r, bucket, objName)
			return
		}
		s.getObjectMeta(w, r, bucket, objName)
	case http.MethodDelete:
		s.deleteObject(w, r, bucket, objName)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// Local shapes for the object-not-found body. errorBody in types.go has no
// errors array and bucket-not-found must keep its plain shape, so these live
// here and stay private to the object paths.
type objectNotFoundResponse struct {
	Error objectNotFoundError `json:"error"`
}

type objectNotFoundError struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Errors  []errorDetail `json:"errors"`
}

type errorDetail struct {
	Domain  string `json:"domain"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// writeObjectNotFound matches the real GCS JSON API response for a missing
// object: a X-Goog-Gcs-Base-Ts header (unix epoch in ms, hex) and an error
// body with an errors array carrying reason "notFound".
func writeObjectNotFound(w http.ResponseWriter, bucket, object string) {
	msg := fmt.Sprintf("No such object: %s/%s", bucket, object)
	w.Header().Set("X-Goog-Gcs-Base-Ts", strconv.FormatInt(time.Now().UnixMilli(), 16))
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(objectNotFoundResponse{
		Error: objectNotFoundError{
			Code:    http.StatusNotFound,
			Message: msg,
			Errors: []errorDetail{
				{Domain: "global", Reason: "notFound", Message: msg},
			},
		},
	})
}

func (s *Service) getObjectMeta(w http.ResponseWriter, _ *http.Request, bucket, object string) {
	data, err := s.store.Get(objectNamespace(bucket), object)
	if errors.Is(err, state.ErrNotFound) {
		writeObjectNotFound(w, bucket, object)
		return
	}
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Service) downloadObject(w http.ResponseWriter, _ *http.Request, bucket, object string) {
	metaRaw, err := s.store.Get(objectNamespace(bucket), object)
	if errors.Is(err, state.ErrNotFound) {
		writeObjectNotFound(w, bucket, object)
		return
	}
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var meta objectResource
	_ = json.Unmarshal(metaRaw, &meta)
	body, err := s.store.Get(objectDataNamespace(bucket), object)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if meta.Etag != "" {
		w.Header().Set("ETag", meta.Etag)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Service) deleteObject(w http.ResponseWriter, _ *http.Request, bucket, object string) {
	if err := s.store.Delete(objectNamespace(bucket), object); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeObjectNotFound(w, bucket, object)
			return
		}
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.store.Delete(objectDataNamespace(bucket), object)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/upload/storage/v1/b/")
	bucket, suffix := splitFirst(rest)
	if !strings.HasPrefix(suffix, "o") {
		s.writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if _, err := s.store.Get(nsBuckets, bucket); err != nil {
		s.writeErr(w, http.StatusNotFound, "bucket not found")
		return
	}

	// Resumable PUT to an established session.
	if uploadID := r.URL.Query().Get("upload_id"); uploadID != "" {
		s.uploadResumableData(w, r, bucket, uploadID)
		return
	}

	if r.Method != http.MethodPost {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	uploadType := r.URL.Query().Get("uploadType")
	switch uploadType {
	case "media":
		s.uploadMedia(w, r, bucket)
	case "multipart":
		s.uploadMultipart(w, r, bucket)
	case "resumable":
		s.uploadResumableStart(w, r, bucket)
	case "":
		s.uploadMedia(w, r, bucket)
	default:
		s.writeErr(w, http.StatusBadRequest, fmt.Sprintf("unsupported uploadType=%q", uploadType))
	}
}

func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	// /download/storage/v1/b/{bucket}/o/{object}?alt=media
	rest := strings.TrimPrefix(r.URL.Path, "/download/storage/v1/b/")
	bucket, suffix := splitFirst(rest)
	if !strings.HasPrefix(suffix, "o/") {
		s.writeErr(w, http.StatusNotFound, "not found")
		return
	}
	objName, err := url.PathUnescape(strings.TrimPrefix(suffix, "o/"))
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid object name")
		return
	}
	s.downloadObject(w, r, bucket, objName)
}
