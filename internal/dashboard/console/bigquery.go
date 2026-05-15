package console

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
)

const bigqueryPageJS = `
window.__page = function () {
  var dsEl = document.getElementById('datasets');
  var tblEl = document.getElementById('tables');
  var qInput = document.getElementById('query');
  var qBtn = document.getElementById('run');
  var qOut = document.getElementById('result');
  var state = { dataset: '' };

  function setActive(ul, name) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.name === name);
    });
  }
  function loadDatasets() {
    return gcp.getJSON('/console/api/bigquery/datasets').then(function (data) {
      dsEl.innerHTML = '';
      (data.datasets || []).forEach(function (d) {
        var li = gcp.el('li', { 'data-name': d.id, text: d.id });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.dataset = d.id; setActive(dsEl, d.id); loadTables(); });
        dsEl.appendChild(li);
      });
    });
  }
  function loadTables() {
    if (!state.dataset) return;
    return gcp.getJSON('/console/api/bigquery/tables?dataset=' + encodeURIComponent(state.dataset)).then(function (data) {
      tblEl.innerHTML = '';
      (data.tables || []).forEach(function (t) {
        var tr = gcp.el('tr', null, [
          gcp.el('td', { text: t.id || '' }),
          gcp.el('td', { text: String(t.fields || 0), class: 'num' }),
          gcp.el('td', { text: t.creationTime || '' }),
        ]);
        tblEl.appendChild(tr);
      });
    });
  }
  qBtn.addEventListener('click', function () {
    var q = qInput.value.trim();
    if (!q) return;
    gcp.postJSON('/console/api/bigquery/query', { query: q }).then(function (data) {
      var rows = data.rows || [];
      var cols = data.columns || [];
      var html = '<table class="compact"><thead><tr>';
      cols.forEach(function (c) { html += '<th>' + c + '</th>'; });
      html += '</tr></thead><tbody>';
      rows.forEach(function (r) {
        html += '<tr>';
        cols.forEach(function (c) { html += '<td>' + (r[c] == null ? '' : String(r[c])) + '</td>'; });
        html += '</tr>';
      });
      html += '</tbody></table>';
      qOut.innerHTML = rows.length === 0 ? '<p class="empty">no rows</p>' : html;
    }).catch(function (e) { qOut.innerHTML = '<p class="empty">error: ' + e.message + '</p>'; });
  });
  loadDatasets();
};
`

func (s *Service) pageBigQuery(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "bigquery.html", "BigQuery", "bigquery", map[string]any{
		"PageScript": template.JS(bigqueryPageJS),
	})
}

type bqQuery struct {
	Query string `json:"query"`
}

func (s *Service) apiBigQuery(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/bigquery/")
	switch rest {
	case "datasets":
		ds, err := s.opts.BigQuery.ConsoleDatasets()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"datasets": ds})
	case "tables":
		dataset := r.URL.Query().Get("dataset")
		if dataset == "" {
			writeErr(w, http.StatusBadRequest, "dataset required")
			return
		}
		tables, err := s.opts.BigQuery.ConsoleTables(dataset)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
	case "query":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		var req bqQuery
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(req.Query) == "" {
			writeErr(w, http.StatusBadRequest, "query required")
			return
		}
		result, err := s.opts.BigQuery.ConsoleQuery(req.Query)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		http.NotFound(w, r)
	}
}
