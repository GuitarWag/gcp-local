package console

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strings"
)

const (
	iamadminPageID = "iamadmin"
	pageScriptKey  = "PageScript"
)

const iamadminPageJS = `
window.__page = function () {
  var accountsEl = document.getElementById('accounts');
  var keysEl = document.getElementById('keys');
  var keysTitle = document.getElementById('keys-title');
  var createForm = document.getElementById('create-form');
  var createOut = document.getElementById('create-out');
  var createKeyForm = document.getElementById('create-key-form');
  var createKeyOut = document.getElementById('create-key-out');
  var state = { email: '' };

  function setActive(ul, key) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.key === key);
    });
  }

  function errorMessage(text) {
    try {
      var body = JSON.parse(text);
      if (body && body.error && body.error.message) return body.error.message;
    } catch (e) { /* not JSON */ }
    return text || 'request failed';
  }

  function del(url) {
    return fetch(url, { method: 'DELETE' }).then(function (r) {
      if (!r.ok) return r.text().then(function (t) { throw new Error(errorMessage(t)); });
      return r.json();
    });
  }

  function loadAccounts() {
    return gcp.getJSON('/console/api/iamadmin/accounts').then(function (data) {
      accountsEl.innerHTML = '';
      (data.accounts || []).forEach(function (a) {
        var delBtn = gcp.el('button', { class: 'secondary', text: 'delete' });
        delBtn.addEventListener('click', function () {
          del('/console/api/iamadmin/accounts?email=' + encodeURIComponent(a.email))
            .then(function () { if (state.email === a.email) { state.email = ''; keysEl.innerHTML = ''; keysTitle.textContent = 'Keys'; } return loadAccounts(); })
            .catch(function (e) { createOut.hidden = false; createOut.textContent = 'error: ' + e.message; });
        });
        var li = gcp.el('li', { 'data-key': a.email, text: a.email }, [delBtn]);
        if (a.displayName) li.appendChild(gcp.el('small', { text: a.displayName }));
        if (a.uniqueId) li.appendChild(gcp.el('small', { text: a.uniqueId }));
        if (a.description) li.appendChild(gcp.el('small', { text: a.description }));
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () {
          state.email = a.email;
          setActive(accountsEl, a.email);
          keysTitle.textContent = 'Keys · ' + a.email;
          loadKeys();
        });
        accountsEl.appendChild(li);
      });
    });
  }

  function loadKeys() {
    if (!state.email) return Promise.resolve();
    return gcp.getJSON('/console/api/iamadmin/keys?account=' + encodeURIComponent(state.email)).then(function (data) {
      keysEl.innerHTML = '';
      (data.keys || []).forEach(function (k) {
        var delBtn = gcp.el('button', { class: 'secondary', text: 'delete' });
        delBtn.addEventListener('click', function () {
          del('/console/api/iamadmin/keys?account=' + encodeURIComponent(state.email) + '&key=' + encodeURIComponent(k.keyId))
            .then(loadKeys)
            .catch(function (e) { createKeyOut.hidden = false; createKeyOut.textContent = 'error: ' + e.message; });
        });
        var dlBtn = gcp.el('button', { class: 'secondary', text: 'download' });
        dlBtn.addEventListener('click', function () {
          window.open('/console/api/iamadmin/keys/' + encodeURIComponent(k.keyId) + '/credentials?account=' + encodeURIComponent(state.email), '_blank');
        });
        var tr = gcp.el('tr', null, [
          gcp.el('td', { text: k.keyId }),
          gcp.el('td', { text: k.keyAlgorithm || '' }),
          gcp.el('td', { text: k.keyType || '' }),
          gcp.el('td', { text: k.validAfterTime || '' }),
          gcp.el('td', { text: k.validBeforeTime || '' }),
          gcp.el('td', null, [delBtn, dlBtn]),
        ]);
        keysEl.appendChild(tr);
      });
    });
  }

  createForm.addEventListener('submit', function (ev) {
    ev.preventDefault();
    var fd = new FormData(createForm);
    gcp.postJSON('/console/api/iamadmin/accounts', {
      accountId: fd.get('accountId') || '',
      displayName: fd.get('displayName') || '',
      description: fd.get('description') || '',
    }).then(function (r) {
      createOut.hidden = false;
      createOut.textContent = 'created ' + (r.email || '');
      return loadAccounts();
    }).catch(function (e) {
      createOut.hidden = false;
      createOut.textContent = 'error: ' + errorMessage(e.message);
    });
  });

  createKeyForm.addEventListener('submit', function (ev) {
    ev.preventDefault();
    if (!state.email) { createKeyOut.hidden = false; createKeyOut.textContent = 'select an account first'; return; }
    gcp.postJSON('/console/api/iamadmin/keys?account=' + encodeURIComponent(state.email), {}).then(function (r) {
      createKeyOut.hidden = false;
      createKeyOut.textContent = 'created key ' + (r.keyId || '');
      return loadKeys();
    }).catch(function (e) {
      createKeyOut.hidden = false;
      createKeyOut.textContent = 'error: ' + errorMessage(e.message);
    });
  });

  gcp.poll(function () {
    return loadAccounts().then(function () {
      setActive(accountsEl, state.email);
      if (state.email) return loadKeys();
    });
  }, 3000);
};
`

func (s *Service) pageIAMAdmin(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "iamadmin.html", "IAM Admin", iamadminPageID, map[string]any{
		pageScriptKey: template.JS(iamadminPageJS),
	})
}

func (s *Service) apiIAMAdmin(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/iamadmin/")
	if strings.HasPrefix(rest, "keys/") && strings.HasSuffix(rest, "/credentials") {
		keyID := strings.TrimSuffix(strings.TrimPrefix(rest, "keys/"), "/credentials")
		s.apiIAMAdminCredentials(w, r, keyID)
		return
	}
	switch rest {
	case "accounts":
		s.apiIAMAdminAccounts(w, r)
	case "keys":
		s.apiIAMAdminKeys(w, r)
	default:
		http.NotFound(w, r)
	}
}

// adapterWriteErr maps an adapter error into the GCP-style error body
// via errors.As on interface{ StatusCode() int }; untyped errors map to 500.
func adapterWriteErr(w http.ResponseWriter, err error) {
	var coder interface{ StatusCode() int }
	code := http.StatusInternalServerError
	if errors.As(err, &coder) {
		code = coder.StatusCode()
	}
	writeErr(w, code, err.Error())
}

func (s *Service) apiIAMAdminAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		accounts, err := s.opts.IAMAdmin.ConsoleAccounts()
		if err != nil {
			adapterWriteErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
	case http.MethodPost:
		var req struct {
			AccountID   string `json:"accountId"`
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		account, err := s.opts.IAMAdmin.ConsoleCreateAccount(req.AccountID, req.DisplayName, req.Description)
		if err != nil {
			adapterWriteErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, account)
	case http.MethodDelete:
		email := r.URL.Query().Get("email")
		if email == "" {
			writeErr(w, http.StatusBadRequest, "email required")
			return
		}
		if err := s.opts.IAMAdmin.ConsoleDeleteAccount(email); err != nil {
			adapterWriteErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) apiIAMAdminKeys(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	key := r.URL.Query().Get("key")
	switch r.Method {
	case http.MethodGet:
		if account == "" {
			writeErr(w, http.StatusBadRequest, "account required")
			return
		}
		if key != "" {
			writeErr(w, http.StatusBadRequest, "key parameter not supported")
			return
		}
		keys, err := s.opts.IAMAdmin.ConsoleKeys(account)
		if err != nil {
			adapterWriteErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
	case http.MethodPost:
		if account == "" {
			writeErr(w, http.StatusBadRequest, "account required")
			return
		}
		created, err := s.opts.IAMAdmin.ConsoleCreateKey(account)
		if err != nil {
			adapterWriteErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	case http.MethodDelete:
		if account == "" || key == "" {
			writeErr(w, http.StatusBadRequest, "account and key required")
			return
		}
		if err := s.opts.IAMAdmin.ConsoleDeleteKey(account, key); err != nil {
			adapterWriteErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// apiIAMAdminCredentials serves the decoded credentials file. keyID comes
// from the sub-path form /keys/<key>/credentials.
func (s *Service) apiIAMAdminCredentials(w http.ResponseWriter, r *http.Request, keyID string) {
	account := r.URL.Query().Get("account")
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if account == "" || keyID == "" {
		writeErr(w, http.StatusBadRequest, "account and key required")
		return
	}
	data, filename, err := s.opts.IAMAdmin.ConsoleCredentials(account, keyID)
	if err != nil {
		adapterWriteErr(w, err)
		return
	}
	var cred any
	if err := json.Unmarshal(data, &cred); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(cred)
}
