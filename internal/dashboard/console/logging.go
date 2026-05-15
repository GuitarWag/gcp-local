package console

import (
	"html/template"
	"net/http"
	"strconv"
)

const loggingPageJS = `
window.__page = function () {
  var tail = document.getElementById('log-tail');
  var filterEl = document.getElementById('filter');
  var statusEl = document.getElementById('status');
  var pauseEl = document.getElementById('pause');
  var paused = false;
  var hovered = false;
  var seen = new Set();

  tail.addEventListener('mouseenter', function () { hovered = true; });
  tail.addEventListener('mouseleave', function () { hovered = false; });
  pauseEl.addEventListener('click', function () {
    paused = !paused;
    pauseEl.textContent = paused ? 'Resume' : 'Pause';
  });

  function fetchEntries() {
    if (paused || hovered) { statusEl.textContent = paused ? 'paused' : 'hover paused'; return; }
    var url = '/console/api/logging/entries?limit=200';
    var f = filterEl.value.trim();
    if (f) url += '&filter=' + encodeURIComponent(f);
    return gcp.getJSON(url).then(function (data) {
      var entries = data.entries || [];
      var added = 0;
      for (var i = 0; i < entries.length; i++) {
        var e = entries[i];
        var key = e.insertId || (e.timestamp + '|' + i);
        if (seen.has(key)) continue;
        seen.add(key);
        var row = gcp.el('div', { class: 'log-row s-' + (e.severity || 'DEFAULT') }, [
          gcp.el('span', { class: 'ts', text: e.timestamp || '' }),
          gcp.el('span', { class: 'sev', text: e.severity || 'DEFAULT' }),
          gcp.el('span', { class: 'msg', text: e.message || '' }),
        ]);
        tail.appendChild(row);
        added++;
      }
      while (tail.children.length > 500) tail.removeChild(tail.firstChild);
      if (added > 0 && !hovered) tail.scrollTop = tail.scrollHeight;
      statusEl.textContent = entries.length + ' loaded · ' + tail.children.length + ' shown';
    }).catch(function (e) { statusEl.textContent = 'error: ' + e.message; });
  }

  gcp.poll(fetchEntries, 1000);
};
`

func (s *Service) pageLogging(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "logging.html", "Cloud Logging", "logging", map[string]any{
		"PageScript": template.JS(loggingPageJS),
	})
}

func (s *Service) apiLogging(w http.ResponseWriter, r *http.Request) {
	// We only expose one sub-route today: entries. Extending here keeps
	// the URL space flat and predictable.
	if r.URL.Path != "/console/api/logging/entries" {
		http.NotFound(w, r)
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	filter := r.URL.Query().Get("filter")
	entries, err := s.opts.Logging.ConsoleEntries(limit, filter)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
