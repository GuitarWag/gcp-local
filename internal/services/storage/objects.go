package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

func (s *Service) getObjectMeta(w http.ResponseWriter, _ *http.Request, bucket, object string) {
	data, err := s.store.Get(objectNamespace(bucket), object)
	if errors.Is(err, state.ErrNotFound) {
		s.writeErr(w, http.StatusNotFound, "object not found")
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
		s.writeErr(w, http.StatusNotFound, "object not found")
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
			s.writeErr(w, http.StatusNotFound, "object not found")
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
	bucket, suffix := splitFirst(rest, "/")
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

type resumableSession struct {
	Bucket      string
	Name        string
	ContentType string
}

var (
	resumableMu       sync.Mutex
	resumableSessions = map[string]*resumableSession{}
	resumableSeq      uint64
)

func (s *Service) uploadResumableStart(w http.ResponseWriter, r *http.Request, bucket string) {
	var meta struct {
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
	}
	// metadata is optional
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&meta)
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = meta.Name
	}
	if name == "" {
		s.writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	ct := r.Header.Get("X-Upload-Content-Type")
	if ct == "" {
		ct = meta.ContentType
	}

	id := fmt.Sprintf("session-%d", atomic.AddUint64(&resumableSeq, 1))
	resumableMu.Lock()
	resumableSessions[id] = &resumableSession{Bucket: bucket, Name: name, ContentType: ct}
	resumableMu.Unlock()

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	location := fmt.Sprintf("%s://%s/upload/storage/v1/b/%s/o?upload_id=%s", scheme, r.Host, bucket, id)
	w.Header().Set("Location", location)
	w.Header().Set("X-Guploader-UploadID", id)
	w.WriteHeader(http.StatusOK)
}

func (s *Service) uploadResumableData(w http.ResponseWriter, r *http.Request, bucket, uploadID string) {
	resumableMu.Lock()
	sess, ok := resumableSessions[uploadID]
	resumableMu.Unlock()
	if !ok || sess.Bucket != bucket {
		s.writeErr(w, http.StatusNotFound, "upload session not found")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ct := sess.ContentType
	if ct == "" {
		ct = r.Header.Get("Content-Type")
	}
	obj := s.storeObject(bucket, sess.Name, ct, body)
	resumableMu.Lock()
	delete(resumableSessions, uploadID)
	resumableMu.Unlock()
	s.writeJSON(w, http.StatusOK, obj)
}

func (s *Service) uploadMedia(w http.ResponseWriter, r *http.Request, bucket string) {
	name := r.URL.Query().Get("name")
	if name == "" {
		s.writeErr(w, http.StatusBadRequest, "name query parameter required for media upload")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ct := r.Header.Get("Content-Type")
	obj := s.storeObject(bucket, name, ct, body)
	s.writeJSON(w, http.StatusOK, obj)
}

func (s *Service) uploadMultipart(w http.ResponseWriter, r *http.Request, bucket string) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		s.writeErr(w, http.StatusBadRequest, "expected multipart content-type")
		return
	}
	boundary := params["boundary"]
	if boundary == "" {
		s.writeErr(w, http.StatusBadRequest, "missing boundary")
		return
	}

	mr := multipart.NewReader(r.Body, boundary)
	var meta struct {
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
	}
	var payload []byte
	var payloadCT string

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		pct := part.Header.Get("Content-Type")
		data, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			s.writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.HasPrefix(pct, "application/json") {
			_ = json.Unmarshal(data, &meta)
		} else {
			payload = data
			payloadCT = pct
		}
	}
	if meta.Name == "" {
		meta.Name = r.URL.Query().Get("name")
	}
	if meta.Name == "" {
		s.writeErr(w, http.StatusBadRequest, "object name required")
		return
	}
	ct := meta.ContentType
	if ct == "" {
		ct = payloadCT
	}
	obj := s.storeObject(bucket, meta.Name, ct, payload)
	s.writeJSON(w, http.StatusOK, obj)
}

func (s *Service) storeObject(bucket, name, ct string, body []byte) objectResource {
	gen := strconv.FormatUint(atomic.AddUint64(&generation, 1), 10)
	obj := objectResource{
		Kind:           "storage#object",
		ID:             fmt.Sprintf("%s/%s/%s", bucket, name, gen),
		Name:           name,
		Bucket:         bucket,
		Generation:     gen,
		Metageneration: "1",
		ContentType:    ct,
		Size:           strconv.Itoa(len(body)),
		MD5Hash:        computeMD5(body),
		CRC32C:         computeCRC32C(body),
		Etag:           hexEtag(body),
		TimeCreated:    time.Now().UTC(),
		Updated:        time.Now().UTC(),
		StorageClass:   "STANDARD",
	}
	metaJSON, _ := json.Marshal(obj)
	_ = s.store.Put(objectNamespace(bucket), name, metaJSON)
	_ = s.store.Put(objectDataNamespace(bucket), name, body)
	return obj
}

func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	// /download/storage/v1/b/{bucket}/o/{object}?alt=media
	rest := strings.TrimPrefix(r.URL.Path, "/download/storage/v1/b/")
	bucket, suffix := splitFirst(rest, "/")
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
