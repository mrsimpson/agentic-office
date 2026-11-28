(function () {
  var main = document.getElementById("stage");
  var status = document.getElementById("status");
  var log = document.getElementById("utterances");
  var tabs = document.getElementById("artifacts");
  var shown = document.getElementById("artifact");
  var own = null, shared = null, sharedHTML = shown.innerHTML, pulse = null;
  function sharedFocus() { var f = tabs.querySelector(".tab.focused"); return f ? f.getAttribute("data-artifact") : null; }
  function mark() {
    tabs.querySelectorAll(".tab").forEach(function (el) {
      var p = el.getAttribute("data-artifact");
      el.classList.toggle("own", own !== null && p === own);
      el.classList.toggle("pulse", own !== null && p === pulse);
      el.setAttribute("aria-selected", String(own === null ? el.classList.contains("focused") : p === own));
    });
  }
  function follow() { own = null; pulse = null; shown.innerHTML = sharedHTML; mark(); }
  function open(p) {
    if (p === sharedFocus()) { follow(); return; }
    own = p; pulse = null; mark();
    load(p);
  }
  function load(p) {
    fetch(main.getAttribute("data-artifact") + "&path=" + encodeURIComponent(p), { credentials: "same-origin" })
      .then(function (r) { return r.ok ? r.text() : Promise.reject(r.status); })
      .then(function (html) { if (own === p) { shown.innerHTML = html; } })
      .catch(function () { if (own === p) { shown.innerHTML = '<p class="note">This artifact cannot be shown here.</p>'; } });
  }
  function pick(e) {
    var el = e.target.closest(".tab");
    if (!el || (e.type === "keydown" && e.key !== "Enter" && e.key !== " ")) { return; }
    e.preventDefault();
    open(el.getAttribute("data-artifact"));
  }
  tabs.addEventListener("click", pick);
  tabs.addEventListener("keydown", pick);
  var jump = document.getElementById("jump");
  function atEnd() { return log.scrollHeight - log.scrollTop - log.clientHeight < 32; }
  function toEnd() { log.scrollTop = log.scrollHeight; jump.hidden = true; }
  log.addEventListener("scroll", function () { if (atEnd()) { jump.hidden = true; } });
  jump.addEventListener("click", toEnd);
  toEnd();
  var es = new EventSource(main.getAttribute("data-events"));
  ["status", "origin", "participants", "artifactlog"].forEach(function (name) {
    es.addEventListener(name, function (e) { document.getElementById(name).innerHTML = e.data; });
  });
  es.addEventListener("artifacts", function (e) {
    tabs.innerHTML = e.data;
    var now = sharedFocus();
    if (own !== null && now !== shared) { pulse = now; }
    if (own !== null && own === now) { follow(); }
    shared = now;
    mark();
  });
  es.addEventListener("artifactlog", function () { if (own !== null) { load(own); } });
  es.addEventListener("artifact", function (e) {
    sharedHTML = e.data;
    if (own === null) { shown.innerHTML = e.data; }
  });
  es.addEventListener("utterances", function (e) {
    var stay = atEnd(), top = log.scrollTop;
    log.innerHTML = e.data;
    if (stay) { toEnd(); } else { log.scrollTop = top; jump.hidden = false; }
  });
  es.addEventListener("end", function () { es.close(); });
  es.onerror = function () {
    status.textContent = es.readyState === EventSource.CLOSED ? "Not connected. Reload the page." : "Reconnecting …";
  };
})();