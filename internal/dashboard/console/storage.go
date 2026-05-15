package console

import (
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const storagePageJS = `
window.__page = function () {
  var bucketsEl = document.getElementById('buckets');
  var objectsEl = document.getElementById('objects');
  var previewTitle = document.getElementById('preview-title');
  var previewMeta = document.getElementById('preview-meta');
  var previewBody = document.getElementById('preview-body');
  var uploadForm = document.getElementById('upload');
  var uploadStatus = document.getElementById('upload-status');
  var state = { bucket: '', object: '' };

  function setActive(ul, name) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.name === name);
    });
  }

  function loadBuckets() {
    return gcp.getJSON('/console/api/storage/buckets').then(function (data) {
      bucketsEl.innerHTML = '';
      (data.buckets || []).forEach(function (b) {
        var li = gcp.el('li', { 'data-name': b.name, text: b.name + '  (' + (b.class || '-') + ')' });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.bucket = b.name; state.object = ''; setActive(bucketsEl, b.name); loadObjects(); previewBody.textContent = 'Select an object to preview.'; previewMeta.innerHTML = ''; previewTitle.textContent = 'Preview'; });
        bucketsEl.appendChild(li);
      });
    });
  }
  function loadObjects() {
    if (!state.bucket) { objectsEl.innerHTML = ''; return; }
    return gcp.getJSON('/console/api/storage/objects?bucket=' + encodeURIComponent(state.bucket)).then(function (data) {
      objectsEl.innerHTML = '';
      (data.objects || []).forEach(function (o) {
        var li = gcp.el('li', { 'data-name': o.name, text: o.name + '  (' + (o.size || '0') + 'B)' });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.object = o.name; setActive(objectsEl, o.name); loadPreview(); });
        objectsEl.appendChild(li);
      });
    });
  }
  function loadPreview() {
    if (!state.bucket || !state.object) return;
    previewTitle.textContent = 'Preview · ' + state.object;
    return gcp.getJSON('/console/api/storage/preview?bucket=' + encodeURIComponent(state.bucket) + '&object=' + encodeURIComponent(state.object) + '&maxBytes=8192').then(function (data) {
      previewMeta.innerHTML = '';
      previewMeta.appendChild(gcp.el('span', null, [gcp.el('b', { text: 'mode' }), data.isText ? 'text' : 'hex']));
      previewBody.textContent = data.body || '';
    }).catch(function (e) { previewBody.textContent = 'error: ' + e.message; });
  }

  uploadForm.addEventListener('submit', function (ev) {
    ev.preventDefault();
    if (!state.bucket) { uploadStatus.textContent = 'select a bucket first'; return; }
    var fd = new FormData(uploadForm);
    var obj = fd.get('object'), body = fd.get('body') || '';
    fetch('/console/api/storage/upload?bucket=' + encodeURIComponent(state.bucket) + '&object=' + encodeURIComponent(obj), {
      method: 'POST',
      headers: { 'content-type': 'text/plain' },
      body: body,
    }).then(function (r) {
      if (!r.ok) return r.text().then(function (t) { throw new Error(t); });
      uploadStatus.textContent = 'uploaded ' + obj;
      loadObjects();
    }).catch(function (e) { uploadStatus.textContent = 'error: ' + e.message; });
  });

  loadBuckets();
};
`

func (s *Service) pageStorage(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "storage.html", "Cloud Storage", "storage", map[string]any{
		"PageScript": template.JS(storagePageJS),
	})
}

func (s *Service) apiStorage(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/storage/")
	switch rest {
	case "buckets":
		buckets, err := s.opts.Storage.ConsoleBuckets()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"buckets": buckets})
	case "objects":
		bucket := r.URL.Query().Get("bucket")
		if bucket == "" {
			writeErr(w, http.StatusBadRequest, "bucket required")
			return
		}
		objs, err := s.opts.Storage.ConsoleObjects(bucket)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"objects": objs})
	case "preview":
		bucket := r.URL.Query().Get("bucket")
		object := r.URL.Query().Get("object")
		maxBytes := 8192
		if v := r.URL.Query().Get("maxBytes"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1<<20 {
				maxBytes = n
			}
		}
		body, isText, err := s.opts.Storage.ConsoleObjectPreview(bucket, object, maxBytes)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"body": body, "isText": isText})
	case "upload":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		bucket := r.URL.Query().Get("bucket")
		object := r.URL.Query().Get("object")
		// Console upload caps bodies at 1 MiB — the console is for
		// inspection, not bulk loading; large uploads should use the
		// real GCS path.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		ct := r.Header.Get("Content-Type")
		if err := s.opts.Storage.ConsoleUpload(bucket, object, ct, body); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "object": object, "bytes": len(body)})
	default:
		http.NotFound(w, r)
	}
}
