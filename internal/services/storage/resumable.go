package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type resumableSession struct {
	Bucket      string
	Name        string
	ContentType string
	Buffer      []byte // accumulated bytes from chunked PUTs
	TotalSize   int64  // -1 while total is unknown ("*" in Content-Range)
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
	resumableSessions[id] = &resumableSession{
		Bucket:      bucket,
		Name:        name,
		ContentType: ct,
		TotalSize:   -1,
	}
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

// uploadResumableData handles the chunked-upload PUT path for an existing
// session. Supports three request shapes:
//
//   - Single-shot PUT (no Content-Range): finish in one chunk.
//   - Chunked PUT with `Content-Range: bytes <start>-<end>/<total|*>`: append
//     bytes; return 308 (Resume Incomplete) until the final byte arrives,
//     then 200 with object metadata.
//   - Status query with `Content-Range: bytes */<total>` and an empty body:
//     report current upload progress via 308 + Range header (no body), or
//     finalise when the buffer already holds all total bytes.
func (s *Service) uploadResumableData(w http.ResponseWriter, r *http.Request, bucket, uploadID string) {
	resumableMu.Lock()
	sess, ok := resumableSessions[uploadID]
	resumableMu.Unlock()
	if !ok || sess.Bucket != bucket {
		s.writeErr(w, http.StatusNotFound, "upload session not found")
		return
	}

	cr := r.Header.Get("Content-Range")

	// Single-shot: no Content-Range. Read the whole body and finalise.
	if cr == "" {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s.writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.finaliseResumable(w, sess, uploadID, body)
		return
	}

	start, end, total, final, isStatus, perr := parseContentRange(cr)
	if perr != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid Content-Range: "+perr.Error())
		return
	}

	resumableMu.Lock()
	defer resumableMu.Unlock()

	// Status query: "bytes */<total>" with empty body. When the buffer already
	// holds all total bytes, this PUT finalises the upload: the Python SDK
	// ends an unknown-total upload with "bytes */N" after the last chunk.
	// "bytes */*" (total unknown) stays a status query.
	if isStatus {
		if total >= 0 {
			if int64(len(sess.Buffer)) == total {
				s.finaliseResumableLocked(w, sess, uploadID)
				return
			}
			sess.TotalSize = total
		}
		s.writeResumableStatus(w, uploadID, sess)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// "bytes <start>-*/<total>": end is open. Derive it from the actual body
	// length. The Node @google-cloud/storage SDK uses this form (often as
	// "bytes 0-*/*" with chunked transfer-encoding) for single-chunk
	// uploads where the SDK hands the request body off as a stream. By
	// protocol, an open-ended end ("*") means "the body in this request is
	// all I have" — so we treat it as the final chunk.
	if end < 0 {
		end = start + int64(len(body)) - 1
		final = true
	}
	if int64(len(body)) != end-start+1 {
		s.writeErr(w, http.StatusBadRequest, fmt.Sprintf(
			"Content-Range byte count (%d-%d=%d) does not match body length (%d)",
			start, end, end-start+1, len(body)))
		return
	}
	if start != int64(len(sess.Buffer)) {
		// Out-of-order chunk. Real GCS returns 503 with a 308-style retry; we
		// just say where the next chunk should start.
		s.writeResumableStatus(w, uploadID, sess)
		return
	}
	sess.Buffer = append(sess.Buffer, body...)
	if total >= 0 {
		sess.TotalSize = total
	}

	if final {
		// Caller asserts this is the last chunk. Finalise.
		s.finaliseResumableLocked(w, sess, uploadID)
		return
	}
	s.writeResumableStatus(w, uploadID, sess)
}

// parseContentRange parses the four shapes the GCS resumable protocol uses:
//
//	bytes <start>-<end>/<total>     normal chunk with known total
//	bytes <start>-<end>/*           normal chunk with unknown total
//	bytes */<total>                 status query
//	bytes */*                       status query, total unknown
//
// final is true iff total >= 0 and end == total-1. isStatus is true iff the
// body is empty per the "*/N" form (caller decides what to do).
func parseContentRange(s string) (start, end, total int64, final, isStatus bool, err error) {
	const prefix = "bytes "
	if !strings.HasPrefix(s, prefix) {
		err = fmt.Errorf("missing %q prefix", prefix)
		return
	}
	rest := strings.TrimPrefix(s, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		err = errors.New("missing '/' separator")
		return
	}
	rangePart, totalPart := rest[:slash], rest[slash+1:]

	if totalPart == "*" {
		total = -1
	} else {
		t, perr := strconv.ParseInt(totalPart, 10, 64)
		if perr != nil {
			err = fmt.Errorf("invalid total: %w", perr)
			return
		}
		total = t
	}

	if rangePart == "*" {
		isStatus = true
		start, end = -1, -1
		return
	}

	dash := strings.IndexByte(rangePart, '-')
	if dash < 0 {
		err = errors.New("missing '-' in range")
		return
	}
	s1, perr := strconv.ParseInt(rangePart[:dash], 10, 64)
	if perr != nil {
		err = fmt.Errorf("invalid start: %w", perr)
		return
	}
	endStr := rangePart[dash+1:]
	if endStr == "*" {
		// "bytes <start>-*/<total>" — end is determined by the body length;
		// caller fills end in once the body is read.
		start, end = s1, -1
		return
	}
	e1, perr := strconv.ParseInt(endStr, 10, 64)
	if perr != nil {
		err = fmt.Errorf("invalid end: %w", perr)
		return
	}
	start, end = s1, e1
	final = total >= 0 && end == total-1
	return
}

func (s *Service) writeResumableStatus(w http.ResponseWriter, uploadID string, sess *resumableSession) {
	received := int64(len(sess.Buffer))
	if received > 0 {
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", received-1))
	}
	w.Header().Set("X-Guploader-UploadID", uploadID)
	// 308 Resume Incomplete is the GCS-flavoured status for "send more".
	w.WriteHeader(http.StatusPermanentRedirect)
}

func (s *Service) finaliseResumable(w http.ResponseWriter, sess *resumableSession, uploadID string, body []byte) {
	resumableMu.Lock()
	defer resumableMu.Unlock()
	sess.Buffer = append(sess.Buffer, body...)
	s.finaliseResumableLocked(w, sess, uploadID)
}

// finaliseResumableLocked must be called with resumableMu held.
func (s *Service) finaliseResumableLocked(w http.ResponseWriter, sess *resumableSession, uploadID string) {
	ct := sess.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	obj := s.storeObject(sess.Bucket, sess.Name, ct, sess.Buffer)
	delete(resumableSessions, uploadID)
	s.writeJSON(w, http.StatusOK, obj)
}
