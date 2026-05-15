package console

import (
	"html/template"
	"net/http"
	"strings"
)

const secretsPageJS = `
window.__page = function () {
  var secEl = document.getElementById('secrets');
  var verEl = document.getElementById('versions');
  var verTitle = document.getElementById('versions-title');
  var payloadTitle = document.getElementById('payload-title');
  var payloadEl = document.getElementById('payload');
  var state = { secret: '' };

  function setActive(ul, key) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.key === key);
    });
  }
  function loadSecrets() {
    return gcp.getJSON('/console/api/secrets/secrets').then(function (data) {
      secEl.innerHTML = '';
      (data.secrets || []).forEach(function (s) {
        var li = gcp.el('li', { 'data-key': s.name, text: s.id });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.secret = s.name; setActive(secEl, s.name); verTitle.textContent = 'Versions · ' + s.id; loadVersions(); payloadEl.textContent = 'Reveal a version to see its payload.'; });
        secEl.appendChild(li);
      });
    });
  }
  function loadVersions() {
    if (!state.secret) return;
    return gcp.getJSON('/console/api/secrets/versions?secret=' + encodeURIComponent(state.secret)).then(function (data) {
      verEl.innerHTML = '';
      (data.versions || []).forEach(function (v) {
        var btn = gcp.el('button', { class: 'secondary', text: 'reveal' });
        btn.addEventListener('click', function () { revealPayload(v.version); });
        var tr = gcp.el('tr', null, [
          gcp.el('td', { text: String(v.version) }),
          gcp.el('td', { text: v.state || '' }),
          gcp.el('td', { text: v.createTime || '' }),
          gcp.el('td', null, [btn]),
        ]);
        verEl.appendChild(tr);
      });
    });
  }
  function revealPayload(v) {
    payloadTitle.textContent = 'Payload · version ' + v;
    return gcp.getJSON('/console/api/secrets/payload?secret=' + encodeURIComponent(state.secret) + '&version=' + encodeURIComponent(String(v))).then(function (data) {
      payloadEl.textContent = data.payload || '';
    }).catch(function (e) { payloadEl.textContent = 'error: ' + e.message; });
  }
  loadSecrets();
};
`

func (s *Service) pageSecrets(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "secrets.html", "Secret Manager", "secrets", map[string]any{
		"PageScript": template.JS(secretsPageJS),
	})
}

func (s *Service) apiSecrets(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/secrets/")
	switch rest {
	case "secrets":
		secrets, err := s.opts.Secrets.ConsoleSecrets()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"secrets": secrets})
	case "versions":
		secret := r.URL.Query().Get("secret")
		if secret == "" {
			writeErr(w, http.StatusBadRequest, "secret required")
			return
		}
		vers, err := s.opts.Secrets.ConsoleVersions(secret)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": vers})
	case "payload":
		secret := r.URL.Query().Get("secret")
		version := r.URL.Query().Get("version")
		if secret == "" || version == "" {
			writeErr(w, http.StatusBadRequest, "secret and version required")
			return
		}
		payload, err := s.opts.Secrets.ConsoleVersionPayload(secret, version)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"payload": payload})
	default:
		http.NotFound(w, r)
	}
}
