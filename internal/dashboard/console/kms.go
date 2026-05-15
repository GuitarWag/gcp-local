package console

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
)

const kmsPageJS = `
window.__page = function () {
  var ringsEl = document.getElementById('rings');
  var keysEl = document.getElementById('keys');
  var cipherTitle = document.getElementById('cipher-title');
  var ctOut = document.getElementById('ciphertext-out');
  var ptOut = document.getElementById('plaintext-out');
  var encForm = document.getElementById('enc-form');
  var decForm = document.getElementById('dec-form');
  var state = { ring: '', key: '' };

  function setActive(ul, key) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.key === key);
    });
  }
  function loadRings() {
    return gcp.getJSON('/console/api/kms/keyrings').then(function (data) {
      ringsEl.innerHTML = '';
      (data.keyRings || []).forEach(function (k) {
        var li = gcp.el('li', { 'data-key': k.name, text: k.id });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.ring = k.name; state.key = ''; setActive(ringsEl, k.name); loadKeys(); cipherTitle.textContent = 'Encrypt / Decrypt'; });
        ringsEl.appendChild(li);
      });
    });
  }
  function loadKeys() {
    if (!state.ring) { keysEl.innerHTML = ''; return; }
    return gcp.getJSON('/console/api/kms/cryptokeys?keyring=' + encodeURIComponent(state.ring)).then(function (data) {
      keysEl.innerHTML = '';
      (data.cryptoKeys || []).forEach(function (k) {
        var li = gcp.el('li', { 'data-key': k.name, text: k.id });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.key = k.name; setActive(keysEl, k.name); cipherTitle.textContent = 'Encrypt / Decrypt · ' + k.id; });
        keysEl.appendChild(li);
      });
    });
  }
  encForm.addEventListener('submit', function (ev) {
    ev.preventDefault();
    if (!state.key) { ctOut.textContent = 'select a key first'; return; }
    var fd = new FormData(encForm);
    gcp.postJSON('/console/api/kms/encrypt?key=' + encodeURIComponent(state.key), { plaintext: fd.get('plaintext') || '' }).then(function (r) {
      ctOut.textContent = r.ciphertext || '';
    }).catch(function (e) { ctOut.textContent = 'error: ' + e.message; });
  });
  decForm.addEventListener('submit', function (ev) {
    ev.preventDefault();
    if (!state.key) { ptOut.textContent = 'select a key first'; return; }
    var fd = new FormData(decForm);
    gcp.postJSON('/console/api/kms/decrypt?key=' + encodeURIComponent(state.key), { ciphertext: fd.get('ciphertext') || '' }).then(function (r) {
      ptOut.textContent = r.plaintext || '';
    }).catch(function (e) { ptOut.textContent = 'error: ' + e.message; });
  });
  loadRings();
};
`

func (s *Service) pageKMS(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "kms.html", "Cloud KMS", "kms", map[string]any{
		"PageScript": template.JS(kmsPageJS),
	})
}

type kmsEncReq struct {
	Plaintext string `json:"plaintext"`
}
type kmsDecReq struct {
	Ciphertext string `json:"ciphertext"`
}

func (s *Service) apiKMS(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/kms/")
	switch rest {
	case "keyrings":
		rings, err := s.opts.KMS.ConsoleKeyRings()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"keyRings": rings})
	case "cryptokeys":
		ring := r.URL.Query().Get("keyring")
		if ring == "" {
			writeErr(w, http.StatusBadRequest, "keyring required")
			return
		}
		keys, err := s.opts.KMS.ConsoleCryptoKeys(ring)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cryptoKeys": keys})
	case "encrypt":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		key := r.URL.Query().Get("key")
		if key == "" {
			writeErr(w, http.StatusBadRequest, "key required")
			return
		}
		var req kmsEncReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		ct, err := s.opts.KMS.ConsoleEncrypt(key, req.Plaintext)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ciphertext": ct})
	case "decrypt":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		key := r.URL.Query().Get("key")
		if key == "" {
			writeErr(w, http.StatusBadRequest, "key required")
			return
		}
		var req kmsDecReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		pt, err := s.opts.KMS.ConsoleDecrypt(key, req.Ciphertext)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plaintext": pt})
	default:
		http.NotFound(w, r)
	}
}
