package console

import (
	"html/template"
	"net/http"
	"strings"
)

const cloudsqlPageJS = `
window.__page = function () {
  var tEl = document.getElementById('instances');
  function load() {
    return gcp.getJSON('/console/api/cloudsql/instances').then(function (data) {
      tEl.innerHTML = '';
      (data.instances || []).forEach(function (i) {
        var tr = gcp.el('tr', null, [
          gcp.el('td', { text: i.name || '' }),
          gcp.el('td', { text: i.engine || '-' }),
          gcp.el('td', { text: String(i.port || ''), class: 'num' }),
          gcp.el('td', { text: i.database || '-' }),
          gcp.el('td', null, [gcp.el('span', { class: 'pill ' + (i.state === 'RUNNABLE' ? 'ok' : 'muted'), text: i.state || 'unknown' })]),
        ]);
        tEl.appendChild(tr);
      });
    });
  }
  gcp.poll(load, 3000);
};
`

func (s *Service) pageCloudSQL(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "cloudsql.html", "Cloud SQL", "cloudsql", map[string]any{
		"PageScript": template.JS(cloudsqlPageJS),
	})
}

func (s *Service) apiCloudSQL(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/instances") {
		http.NotFound(w, r)
		return
	}
	insts, err := s.opts.CloudSQL.ConsoleInstances()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": insts})
}
