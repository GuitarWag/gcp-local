package console

import (
	"html/template"
	"net/http"
	"strings"
)

const monitoringPageJS = `
window.__page = function () {
  var tEl = document.getElementById('series');
  function load() {
    return gcp.getJSON('/console/api/monitoring/series?limit=200').then(function (data) {
      tEl.innerHTML = '';
      (data.series || []).forEach(function (s) {
        var tr = gcp.el('tr', null, [
          gcp.el('td', { text: s.metricType || '' }),
          gcp.el('td', { text: s.resourceType || '' }),
          gcp.el('td', { text: s.metricKind || '-' }),
          gcp.el('td', { text: s.valueType || '-' }),
          gcp.el('td', { text: String(s.points || 0), class: 'num' }),
          gcp.el('td', { text: s.lastEndTime || '' }),
        ]);
        tEl.appendChild(tr);
      });
    });
  }
  gcp.poll(load, 3000);
};
`

func (s *Service) pageMonitoring(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "monitoring.html", "Cloud Monitoring", "monitoring", map[string]any{
		"PageScript": template.JS(monitoringPageJS),
	})
}

func (s *Service) apiMonitoring(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/series") {
		http.NotFound(w, r)
		return
	}
	series, err := s.opts.Monitoring.ConsoleSeries(200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series})
}
