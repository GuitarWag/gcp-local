package console

import (
	"html/template"
	"net/http"
	"strings"
)

// Cloud Run and Cloud Functions share the same Service type — only the
// labels and underlying namespace differ. We render them with the same
// template parameterised by Kind ("service" or "function").

const cloudrunPageJS = `
window.__page = function () {
  var tEl = document.getElementById('resources');
  var basePath = window.__consoleBasePath;
  function load() {
    return gcp.getJSON('/console/api/' + basePath + '/list').then(function (data) {
      tEl.innerHTML = '';
      (data.resources || []).forEach(function (r) {
        var tr = gcp.el('tr', null, [
          gcp.el('td', { text: r.name || '' }),
          gcp.el('td', { text: r.backendUrl || '(no backend configured)' }),
          gcp.el('td', { text: r.createTime || '' }),
        ]);
        tEl.appendChild(tr);
      });
    });
  }
  gcp.poll(load, 3000);
};
`

func (s *Service) pageCloudRun(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "cloudrun.html", "Cloud Run", "cloudrun", map[string]any{
		"PageScript":    template.JS("window.__consoleBasePath = 'cloudrun';" + cloudrunPageJS),
		"ResourceLabel": "Services",
		"ResourceNote":  "Cloud Run services. Real Cloud Run boots containers; this emulator proxies invoke requests to a configured backendUrl.",
	})
}

func (s *Service) pageFunctions(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "cloudrun.html", "Cloud Functions", "functions", map[string]any{
		"PageScript":    template.JS("window.__consoleBasePath = 'functions';" + cloudrunPageJS),
		"ResourceLabel": "Functions",
		"ResourceNote":  "Cloud Functions. Same proxy model as Cloud Run; no subprocess execution.",
	})
}

func (s *Service) apiCloudRun(w http.ResponseWriter, r *http.Request) {
	s.cloudrunList(w, r, s.opts.CloudRun)
}
func (s *Service) apiFunctions(w http.ResponseWriter, r *http.Request) {
	s.cloudrunList(w, r, s.opts.Functions)
}

func (s *Service) cloudrunList(w http.ResponseWriter, r *http.Request, svc ConsoleCloudRun) {
	if !strings.HasSuffix(r.URL.Path, "/list") {
		http.NotFound(w, r)
		return
	}
	res, err := svc.ConsoleResources()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": res})
}
