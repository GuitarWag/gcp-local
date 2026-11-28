package console

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const bigtablePageJS = `
window.__page = function () {
  var tblUl = document.getElementById('tables');
  var tnameEl = document.getElementById('tname');
  var famTbl = document.getElementById('families');
  var rowsTbl = document.getElementById('rows');
  var detailEl = document.getElementById('rowdetail');
  var detailHead = document.getElementById('rowdetail-head');
  var emptyEl = document.getElementById('no-tables');
  var detailPanes = document.getElementById('detail-panes');
  var state = { table: '', row: '' };

  function setActive(ul, name) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.name === name);
    });
  }

  function loadTables() {
    return gcp.getJSON('/console/api/bigtable/tables').then(function (data) {
      var tables = data.tables || [];
      emptyEl.hidden = tables.length > 0;
      detailPanes.hidden = tables.length === 0;
      if (tables.length === 0) {
        state.table = '';
        state.row = '';
        tblUl.innerHTML = '';
        return;
      }
      if (!state.table || !tables.some(function (t) { return t.name === state.table; })) {
        state.table = tables[0].name;
        state.row = '';
      }
      tblUl.innerHTML = '';
      tables.forEach(function (t) {
        var li = gcp.el('li', { 'data-name': t.name }, [
          gcp.el('span', { text: t.id }),
          gcp.el('small', { text: (t.families || []).length + ' fam · ' + String(t.rows || 0) + ' rows' }),
        ]);
        li.addEventListener('click', function () {
          state.table = t.name;
          state.row = '';
          setActive(tblUl, t.name);
          loadRows();
        });
        tblUl.appendChild(li);
      });
      setActive(tblUl, state.table);
      return loadRows();
    });
  }

  function fmtSize(n) {
    if (n == null) return '';
    if (n < 1024) return n + ' B';
    if (n < 1048576) return (n / 1024).toFixed(1) + ' KiB';
    return (n / 1048576).toFixed(1) + ' MiB';
  }

  function loadRows() {
    tnameEl.textContent = state.table;
    if (!state.table) return;
    return gcp.getJSON('/console/api/bigtable/rows?table=' + encodeURIComponent(state.table)).then(function (data) {
      rowsTbl.innerHTML = '';
      (data.rows || []).forEach(function (r) {
        var tr = gcp.el('tr', { 'data-key': r.key }, [
          gcp.el('td', { text: r.key, class: 'muted' }),
          gcp.el('td', { text: String(r.cellCount || 0), class: 'num' }),
          gcp.el('td', { text: fmtSize(r.size), class: 'num' }),
        ]);
        tr.addEventListener('click', function () {
          state.row = r.key;
          showRowDetail(r);
        });
        rowsTbl.appendChild(tr);
      });
      if (state.row) {
        var sel = (data.rows || []).filter(function (r) { return r.key === state.row; })[0];
        if (sel) showRowDetail(sel);
        else { state.row = ''; detailEl.innerHTML = ''; detailHead.textContent = 'Row detail'; }
      } else {
        detailEl.innerHTML = '';
        detailHead.textContent = 'Row detail';
      }
    });
  }

  function showRowDetail(r) {
    setActive(rowsTbl, r.key);
    detailHead.textContent = 'Row detail — ' + r.key;
    detailEl.innerHTML = '';
    (r.families || []).forEach(function (f) {
      detailEl.appendChild(gcp.el('h4', { text: f.name }));
      var tbl = gcp.el('table', { class: 'compact' });
      tbl.appendChild(gcp.el('thead', null, [gcp.el('tr', null, [
        gcp.el('th', { text: 'Qualifier' }),
        gcp.el('th', { text: 'Timestamp (µs)' }),
        gcp.el('th', { text: 'Size' }),
        gcp.el('th', { text: 'Value' }),
      ])]));
      var body = gcp.el('tbody');
      (f.cells || []).forEach(function (c) {
        var valTd = c.value != null ? gcp.el('td', { text: String(c.value), class: 'muted' })
                                    : gcp.el('td', { text: fmtSize(c.size), class: 'num' });
        body.appendChild(gcp.el('tr', null, [
          gcp.el('td', { text: c.qualifier || '', class: 'muted' }),
          gcp.el('td', { text: String(c.ts || 0), class: 'num' }),
          gcp.el('td', { text: fmtSize(c.size), class: 'num' }),
          valTd,
        ]));
      });
      tbl.appendChild(body);
      detailEl.appendChild(tbl);
    });
  }

  gcp.poll(loadTables, 3000);
};
`

func (s *Service) pageBigtable(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "bigtable.html", "Bigtable", "bigtable", map[string]any{
		"PageScript": template.JS(bigtablePageJS),
	})
}

func (s *Service) apiBigtable(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/bigtable/")
	switch rest {
	case "status":
		writeJSON(w, http.StatusOK, s.opts.Bigtable.ConsoleStatus())
	case "tables":
		tables, err := s.opts.Bigtable.ConsoleTables()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
	case "rows":
		table := r.URL.Query().Get("table")
		if table == "" {
			writeErr(w, http.StatusBadRequest, "table required")
			return
		}
		limit := 0
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				writeErr(w, http.StatusBadRequest, "limit must be a positive integer")
				return
			}
			limit = n
		}
		rows, err := s.opts.Bigtable.ConsoleRows(table, limit)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				writeErr(w, http.StatusNotFound, err.Error())
			} else {
				writeErr(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"table": table, "rows": rows})
	default:
		http.NotFound(w, r)
	}
}
