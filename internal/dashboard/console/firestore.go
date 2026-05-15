package console

import (
	"html/template"
	"net/http"
	"strings"
)

const firestorePageJS = `
window.__page = function () {
  var collEl = document.getElementById('collections');
  var docsEl = document.getElementById('docs');
  var docTitle = document.getElementById('doc-title');
  var docMeta = document.getElementById('doc-meta');
  var docFields = document.getElementById('doc-fields');
  var state = { collection: '', doc: '' };

  function setActive(ul, key) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.key === key);
    });
  }
  function loadCollections() {
    return gcp.getJSON('/console/api/firestore/collections').then(function (data) {
      collEl.innerHTML = '';
      (data.collections || []).forEach(function (c) {
        var li = gcp.el('li', { 'data-key': c, text: c });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.collection = c; state.doc = ''; setActive(collEl, c); loadDocs(); docFields.textContent = 'Select a document.'; docMeta.innerHTML = ''; docTitle.textContent = 'Document'; });
        collEl.appendChild(li);
      });
    });
  }
  function loadDocs() {
    if (!state.collection) { docsEl.innerHTML = ''; return; }
    return gcp.getJSON('/console/api/firestore/documents?collection=' + encodeURIComponent(state.collection)).then(function (data) {
      docsEl.innerHTML = '';
      (data.documents || []).forEach(function (d) {
        var li = gcp.el('li', { 'data-key': d.name, text: d.id });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.doc = d.name; setActive(docsEl, d.name); loadDoc(); });
        docsEl.appendChild(li);
      });
    });
  }
  function loadDoc() {
    if (!state.doc) return;
    docTitle.textContent = 'Document · ' + state.doc;
    return gcp.getJSON('/console/api/firestore/document?name=' + encodeURIComponent(state.doc)).then(function (data) {
      docMeta.innerHTML = '';
      docMeta.appendChild(gcp.el('span', null, [gcp.el('b', { text: 'createTime' }), data.createTime]));
      docMeta.appendChild(gcp.el('span', null, [gcp.el('b', { text: 'updateTime' }), data.updateTime]));
      docFields.textContent = JSON.stringify(data.fields || {}, null, 2);
    }).catch(function (e) { docFields.textContent = 'error: ' + e.message; });
  }

  loadCollections();
};
`

func (s *Service) pageFirestore(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "firestore.html", "Firestore", "firestore", map[string]any{
		"PageScript": template.JS(firestorePageJS),
	})
}

func (s *Service) apiFirestore(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/firestore/")
	switch rest {
	case "collections":
		colls, err := s.opts.Firestore.ConsoleCollections()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"collections": colls})
	case "documents":
		coll := r.URL.Query().Get("collection")
		docs, err := s.opts.Firestore.ConsoleDocuments(coll)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"documents": docs})
	case "document":
		name := r.URL.Query().Get("name")
		if name == "" {
			writeErr(w, http.StatusBadRequest, "name required")
			return
		}
		doc, err := s.opts.Firestore.ConsoleDocument(name)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, doc)
	default:
		http.NotFound(w, r)
	}
}
