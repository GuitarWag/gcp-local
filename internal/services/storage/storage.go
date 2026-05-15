package storage

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

func computeCRC32C(data []byte) string {
	sum := crc32.Checksum(data, crc32cTable)
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], sum)
	return base64.StdEncoding.EncodeToString(buf[:])
}

const (
	nsBuckets = "storage/buckets"
)

type Service struct {
	store   state.Store
	project string
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	s := &Service{store: store, project: cfg.Project}
	for _, b := range cfg.Services.Storage.Buckets {
		if err := s.ensureBucket(b.Name); err != nil {
			return nil, fmt.Errorf("seed bucket %s: %w", b.Name, err)
		}
		if b.Seed != "" {
			if err := s.seedBucket(b.Name, b.Seed); err != nil {
				return nil, fmt.Errorf("seed objects for %s: %w", b.Name, err)
			}
		}
	}
	return s, nil
}

func (s *Service) seedBucket(bucket, dir string) error {
	info, err := osStat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("seed path %s is not a directory", dir)
	}
	return walkDir(dir, func(relPath, absPath string) error {
		data, err := readFile(absPath)
		if err != nil {
			return err
		}
		ct := detectContentType(relPath, data)
		s.storeObject(bucket, relPath, ct, data)
		return nil
	})
}

func (s *Service) Name() string { return "storage" }

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/storage/v1/b", s.handleBuckets)
	mux.HandleFunc("/storage/v1/b/", s.handleBucketPath)
	mux.HandleFunc("/upload/storage/v1/b/", s.handleUpload)
	mux.HandleFunc("/download/storage/v1/b/", s.handleDownload)
	// Some SDKs (notably @google-cloud/storage for Node) strip the /storage/v1
	// prefix when apiEndpoint points at an emulator. Mirror the routes at root.
	mux.HandleFunc("/b", s.handleBuckets)
	mux.HandleFunc("/b/", s.handleBucketPath)
	mux.HandleFunc("/upload/b/", s.handleUploadRoot)
	mux.HandleFunc("/download/b/", s.handleDownloadRoot)
}

func (s *Service) handleUploadRoot(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = "/upload/storage/v1" + r.URL.Path[len("/upload"):]
	s.handleUpload(w, r)
}

func (s *Service) handleDownloadRoot(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = "/download/storage/v1" + r.URL.Path[len("/download"):]
	s.handleDownload(w, r)
}

// HandleXML handles XML-style /{bucket}/{object} requests used by the GCS SDK
// (Python google-cloud-storage in particular) when STORAGE_EMULATOR_HOST points
// here. Supports GET (read), PUT (write), and DELETE.
func (s *Service) HandleXML(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		return false
	}
	bucket, object := splitFirst(path)
	if object == "" {
		return false
	}
	if _, err := s.store.Get(nsBuckets, bucket); err != nil {
		return false
	}
	objName, err := url.PathUnescape(object)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid object name")
		return true
	}
	switch r.Method {
	case http.MethodGet:
		s.downloadObject(w, r, bucket, objName)
		return true
	case http.MethodPut:
		s.xmlPutObject(w, r, bucket, objName)
		return true
	case http.MethodDelete:
		s.deleteObject(w, r, bucket, objName)
		return true
	}
	return false
}

// xmlPutObject implements the XML PUT-object upload path. Body is the raw
// object bytes; Content-Type, if provided, becomes the object's content type.
func (s *Service) xmlPutObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ct := r.Header.Get("Content-Type")
	obj := s.storeObject(bucket, object, ct, body)
	if obj.Etag != "" {
		w.Header().Set("ETag", obj.Etag)
	}
	w.Header().Set("x-goog-generation", obj.Generation)
	w.WriteHeader(http.StatusOK)
}

func (s *Service) ensureBucket(name string) error {
	if name == "" {
		return errors.New("empty bucket name")
	}
	_, err := s.store.Get(nsBuckets, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return err
	}
	b := bucketResource{
		Kind:         "storage#bucket",
		ID:           name,
		Name:         name,
		Location:     "US",
		StorageClass: "STANDARD",
		TimeCreated:  time.Now().UTC(),
		Updated:      time.Now().UTC(),
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return s.store.Put(nsBuckets, name, data)
}

func (s *Service) writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func (s *Service) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, errorResponse{Error: errorBody{Code: code, Message: msg}})
}

func (s *Service) handleBuckets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listBuckets(w, r)
	case http.MethodPost:
		s.createBucket(w, r)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) listBuckets(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.List(nsBuckets, "")
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := bucketsList{Kind: "storage#buckets", Items: []bucketResource{}}
	for _, v := range items {
		var b bucketResource
		if err := json.Unmarshal(v, &b); err != nil {
			continue
		}
		out.Items = append(out.Items, b)
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Service) createBucket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Name == "" {
		s.writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	if _, err := s.store.Get(nsBuckets, body.Name); err == nil {
		s.writeErr(w, http.StatusConflict, "bucket already exists")
		return
	}
	b := bucketResource{
		Kind:         "storage#bucket",
		ID:           body.Name,
		Name:         body.Name,
		Location:     "US",
		StorageClass: "STANDARD",
		TimeCreated:  time.Now().UTC(),
		Updated:      time.Now().UTC(),
	}
	data, err := json.Marshal(b)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.Put(nsBuckets, body.Name, data); err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, b)
}

// handleBucketPath handles /storage/v1/b/{bucket}[...] and /b/{bucket}[...].
func (s *Service) handleBucketPath(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/storage/v1/b/"):
		path = strings.TrimPrefix(path, "/storage/v1/b/")
	case strings.HasPrefix(path, "/b/"):
		path = strings.TrimPrefix(path, "/b/")
	}
	rest := path
	if rest == "" {
		s.handleBuckets(w, r)
		return
	}
	// Split bucket from rest
	bucket, suffix := splitFirst(rest)
	if suffix == "" {
		// /storage/v1/b/{bucket}
		s.bucketOp(w, r, bucket)
		return
	}
	switch {
	case suffix == "o":
		s.handleObjects(w, r, bucket)
	case strings.HasPrefix(suffix, "o/"):
		object := strings.TrimPrefix(suffix, "o/")
		s.handleObject(w, r, bucket, object)
	default:
		s.writeErr(w, http.StatusNotFound, "not found")
	}
}

func (s *Service) bucketOp(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(nsBuckets, bucket)
		if errors.Is(err, state.ErrNotFound) {
			s.writeErr(w, http.StatusNotFound, "bucket not found")
			return
		}
		if err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.deleteBucketAndObjects(bucket); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				s.writeErr(w, http.StatusNotFound, "bucket not found")
				return
			}
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) deleteBucketAndObjects(bucket string) error {
	if _, err := s.store.Get(nsBuckets, bucket); err != nil {
		return err
	}
	// remove all objects in this bucket
	objNs := objectNamespace(bucket)
	all, _ := s.store.List(objNs, "")
	for k := range all {
		_ = s.store.Delete(objNs, k)
		_ = s.store.Delete(objectDataNamespace(bucket), k)
	}
	return s.store.Delete(nsBuckets, bucket)
}

func objectNamespace(bucket string) string {
	return "storage/objects/" + bucket
}

func objectDataNamespace(bucket string) string {
	return "storage/data/" + bucket
}

func splitFirst(s string) (string, string) {
	i := strings.IndexByte(s, '/')
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func computeMD5(data []byte) string {
	sum := md5.Sum(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func hexEtag(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
