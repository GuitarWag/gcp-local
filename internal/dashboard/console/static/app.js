// gcp-local console — light vanilla JS for polling pages.
// Each page sets window.__page to a function that runs on DOMContentLoaded.

(function () {
  function getJSON(url) {
    return fetch(url, { headers: { accept: 'application/json' } }).then(function (r) {
      if (!r.ok) throw new Error(url + ' -> ' + r.status);
      return r.json();
    });
  }

  function postJSON(url, body) {
    return fetch(url, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body || {}),
    }).then(function (r) {
      if (!r.ok) return r.text().then(function (t) { throw new Error(t || r.statusText); });
      return r.json().catch(function () { return {}; });
    });
  }

  function el(tag, props, children) {
    var e = document.createElement(tag);
    if (props) for (var k in props) {
      if (k === 'class') e.className = props[k];
      else if (k === 'text') e.textContent = props[k];
      else if (k === 'html') e.innerHTML = props[k];
      else if (k.startsWith('on') && typeof props[k] === 'function') e.addEventListener(k.slice(2), props[k]);
      else e.setAttribute(k, props[k]);
    }
    if (children) for (var i = 0; i < children.length; i++) {
      var c = children[i];
      if (c == null) continue;
      e.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    }
    return e;
  }

  function poll(fn, ms) {
    var stopped = false;
    function tick() {
      if (stopped) return;
      Promise.resolve(fn()).catch(function () { /* swallow */ }).then(function () {
        if (!stopped) setTimeout(tick, ms);
      });
    }
    tick();
    return function () { stopped = true; };
  }

  window.gcp = { getJSON: getJSON, postJSON: postJSON, el: el, poll: poll };

  document.addEventListener('DOMContentLoaded', function () {
    if (typeof window.__page === 'function') {
      try { window.__page(); } catch (e) { console.error(e); }
    }
  });
})();
