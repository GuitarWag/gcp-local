package console

import (
	"html/template"
	"net/http"
	"strings"
)

// Both Bigtable and Spanner are stub services. They render the same
// status card and a short note about what's implemented.

const stubPageJS = `
window.__page = function () {
  var basePath = window.__consoleBasePath;
  var noteEl = document.getElementById('note');
  var surfaceEl = document.getElementById('surface');
  var projectEl = document.getElementById('project');
  gcp.getJSON('/console/api/' + basePath + '/status').then(function (data) {
    noteEl.textContent = data.note || '';
    surfaceEl.textContent = data.surface || '';
    projectEl.textContent = data.project || '';
  });
};
`

func (s *Service) pageBigtable(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "stubservice.html", "Bigtable", "bigtable", map[string]any{
		"PageScript": template.JS("window.__consoleBasePath = 'bigtable';" + stubPageJS),
	})
}

func (s *Service) pageSpanner(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "stubservice.html", "Spanner", "spanner", map[string]any{
		"PageScript": template.JS("window.__consoleBasePath = 'spanner';" + stubPageJS),
	})
}

func (s *Service) apiBigtable(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/status") {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Bigtable.ConsoleStatus())
}

func (s *Service) apiSpanner(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/status") {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Spanner.ConsoleStatus())
}
