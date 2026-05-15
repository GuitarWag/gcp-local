package console

import (
	"html/template"
	"net/http"
	"strings"
)

const memorystorePageJS = `
window.__page = function () {
  var hostEl = document.getElementById('host');
  var portEl = document.getElementById('port');
  var countEl = document.getElementById('keycount');
  var listEl = document.getElementById('keys');
  function load() {
    return gcp.getJSON('/console/api/memorystore/status').then(function (data) {
      hostEl.textContent = data.host || '127.0.0.1';
      portEl.textContent = String(data.port || '');
      countEl.textContent = String(data.keyCount || 0);
      listEl.innerHTML = '';
      (data.keys || []).forEach(function (k) {
        listEl.appendChild(gcp.el('li', { text: k }));
      });
    });
  }
  gcp.poll(load, 2000);
};
`

func (s *Service) pageMemorystore(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "memorystore.html", "Memorystore", "memorystore", map[string]any{
		"PageScript": template.JS(memorystorePageJS),
	})
}

func (s *Service) apiMemorystore(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/status") {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Memorystore.ConsoleStatus())
}
