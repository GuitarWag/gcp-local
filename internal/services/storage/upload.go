package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

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
		if errors.Is(err, io.EOF) {
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

const storageClassStandard = "STANDARD"

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
		StorageClass:   storageClassStandard,
	}
	metaJSON, _ := json.Marshal(obj)
	_ = s.store.Put(objectNamespace(bucket), name, metaJSON)
	_ = s.store.Put(objectDataNamespace(bucket), name, body)
	return obj
}
