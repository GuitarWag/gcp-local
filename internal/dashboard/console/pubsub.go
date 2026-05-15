package console

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

const pubsubPageJS = `
window.__page = function () {
  var topicsEl = document.getElementById('topics');
  var subsEl = document.getElementById('subs');
  var peekEl = document.getElementById('peek');
  var peekTitle = document.getElementById('peek-title');
  var pubForm = document.getElementById('publish');
  var pubStatus = document.getElementById('publish-status');
  var state = { topic: '', sub: '' };

  function setActive(ul, name) {
    Array.prototype.forEach.call(ul.children, function (li) {
      li.classList.toggle('active', li.dataset.name === name);
    });
  }
  function loadTopics() {
    return gcp.getJSON('/console/api/pubsub/topics').then(function (data) {
      topicsEl.innerHTML = '';
      (data.topics || []).forEach(function (t) {
        var li = gcp.el('li', { 'data-name': t.name, text: t.name });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.topic = t.name; state.sub = ''; setActive(topicsEl, t.name); loadSubs(); peekEl.innerHTML = ''; peekTitle.textContent = 'Messages (peek)'; });
        topicsEl.appendChild(li);
      });
    });
  }
  function loadSubs() {
    var url = '/console/api/pubsub/subscriptions';
    if (state.topic) url += '?topic=' + encodeURIComponent(state.topic);
    return gcp.getJSON(url).then(function (data) {
      subsEl.innerHTML = '';
      (data.subscriptions || []).forEach(function (sub) {
        var li = gcp.el('li', { 'data-name': sub.name, text: sub.name + (sub.pushEndpoint ? '  (push)' : '') });
        li.style.cursor = 'pointer';
        li.addEventListener('click', function () { state.sub = sub.name; setActive(subsEl, sub.name); loadPeek(); });
        subsEl.appendChild(li);
      });
    });
  }
  function loadPeek() {
    if (!state.sub) return;
    peekTitle.textContent = 'Messages (peek) · ' + state.sub;
    return gcp.getJSON('/console/api/pubsub/peek?subscription=' + encodeURIComponent(state.sub) + '&limit=20').then(function (data) {
      peekEl.innerHTML = '';
      var msgs = data.messages || [];
      if (msgs.length === 0) { peekEl.appendChild(gcp.el('p', { class: 'empty', text: 'no messages' })); return; }
      msgs.forEach(function (m) {
        var box = gcp.el('div', { class: 'log-row s-INFO' }, [
          gcp.el('span', { class: 'ts', text: m.publishTime || '' }),
          gcp.el('span', { class: 'sev', text: m.messageId || '' }),
          gcp.el('span', { class: 'msg', text: m.data || '' }),
        ]);
        peekEl.appendChild(box);
      });
    }).catch(function (e) { peekEl.innerHTML = '<p class="empty">error: ' + e.message + '</p>'; });
  }
  pubForm.addEventListener('submit', function (ev) {
    ev.preventDefault();
    if (!state.topic) { pubStatus.textContent = 'select a topic first'; return; }
    var fd = new FormData(pubForm);
    gcp.postJSON('/console/api/pubsub/publish?topic=' + encodeURIComponent(state.topic), { data: fd.get('data') || '' }).then(function (r) {
      pubStatus.textContent = 'published ' + r.messageId;
      if (state.sub) loadPeek();
    }).catch(function (e) { pubStatus.textContent = 'error: ' + e.message; });
  });

  loadTopics();
  gcp.poll(function () { if (state.sub) return loadPeek(); }, 2000);
};
`

func (s *Service) pagePubSub(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "pubsub.html", "Pub/Sub", "pubsub", map[string]any{
		"PageScript": template.JS(pubsubPageJS),
	})
}

type pubsubPublishReq struct {
	Data       string            `json:"data"`
	Attributes map[string]string `json:"attributes"`
}

func (s *Service) apiPubSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/pubsub/")
	switch rest {
	case "topics":
		topics, err := s.opts.PubSub.ConsoleTopics()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"topics": topics})
	case "subscriptions":
		subs, err := s.opts.PubSub.ConsoleSubscriptions(r.URL.Query().Get("topic"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
	case "peek":
		sub := r.URL.Query().Get("subscription")
		if sub == "" {
			writeErr(w, http.StatusBadRequest, "subscription required")
			return
		}
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		msgs, err := s.opts.PubSub.ConsolePeekMessages(sub, limit)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
	case "publish":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		topic := r.URL.Query().Get("topic")
		if topic == "" {
			writeErr(w, http.StatusBadRequest, "topic required")
			return
		}
		var req pubsubPublishReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		id, err := s.opts.PubSub.ConsolePublish(topic, []byte(req.Data), req.Attributes)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messageId": id})
	default:
		http.NotFound(w, r)
	}
}
