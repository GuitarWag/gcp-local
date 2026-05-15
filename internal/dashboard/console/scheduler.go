package console

import (
	"html/template"
	"net/http"
	"strings"
)

const schedulerPageJS = `
window.__page = function () {
  var jEl = document.getElementById('jobs');
  function load() {
    return gcp.getJSON('/console/api/scheduler/jobs').then(function (data) {
      jEl.innerHTML = '';
      (data.jobs || []).forEach(function (j) {
        var tr = gcp.el('tr', null, [
          gcp.el('td', { text: j.name || '' }),
          gcp.el('td', { text: j.schedule || '' }),
          gcp.el('td', { text: j.nextFire || '' }),
          gcp.el('td', { text: j.method || '' }),
          gcp.el('td', { text: j.uri || '' }),
        ]);
        jEl.appendChild(tr);
      });
    });
  }
  gcp.poll(load, 3000);
};
`

func (s *Service) pageScheduler(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "scheduler.html", "Cloud Scheduler", "scheduler", map[string]any{
		"PageScript": template.JS(schedulerPageJS),
	})
}

func (s *Service) apiScheduler(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/scheduler/")
	switch rest {
	case "jobs":
		jobs, err := s.opts.Scheduler.ConsoleJobs()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
	default:
		http.NotFound(w, r)
	}
}
