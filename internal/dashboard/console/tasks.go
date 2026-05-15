package console

import (
	"html/template"
	"net/http"
	"strings"
)

const tasksPageJS = `
window.__page = function () {
  var qEl = document.getElementById('queues');
  var tEl = document.getElementById('tasks');
  var tTitle = document.getElementById('tasks-title');
  var state = { queue: '' };

  function setActive(ul, key) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.key === key);
    });
  }
  function loadQueues() {
    return gcp.getJSON('/console/api/tasks/queues').then(function (data) {
      qEl.innerHTML = '';
      (data.queues || []).forEach(function (q) {
        var li = gcp.el('li', { 'data-key': q.name, text: q.id });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.queue = q.name; setActive(qEl, q.name); tTitle.textContent = 'Tasks · ' + q.id; loadTasks(); });
        qEl.appendChild(li);
      });
    });
  }
  function loadTasks() {
    if (!state.queue) return;
    return gcp.getJSON('/console/api/tasks/tasks?queue=' + encodeURIComponent(state.queue)).then(function (data) {
      tEl.innerHTML = '';
      (data.tasks || []).forEach(function (t) {
        var tr = gcp.el('tr', null, [
          gcp.el('td', { text: t.id || '' }),
          gcp.el('td', { text: t.method || '' }),
          gcp.el('td', { text: t.url || '' }),
          gcp.el('td', { text: t.scheduleTime || '' }),
        ]);
        tEl.appendChild(tr);
      });
    });
  }
  loadQueues();
  gcp.poll(function () { if (state.queue) return loadTasks(); }, 2000);
};
`

func (s *Service) pageTasks(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "tasks.html", "Cloud Tasks", "tasks", map[string]any{
		"PageScript": template.JS(tasksPageJS),
	})
}

func (s *Service) apiTasks(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/tasks/")
	switch rest {
	case "queues":
		qs, err := s.opts.Tasks.ConsoleQueues()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"queues": qs})
	case "tasks":
		q := r.URL.Query().Get("queue")
		if q == "" {
			writeErr(w, http.StatusBadRequest, "queue required")
			return
		}
		ts, err := s.opts.Tasks.ConsoleTasks(q)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": ts})
	default:
		http.NotFound(w, r)
	}
}
