// Live tile updates.
//
// The server pushes a bare notification when something changes state and this
// fetches the tile fragment, rather than the event carrying the HTML. That
// keeps one renderer instead of two and means a reconnecting client gets
// current state rather than replaying a stream of deltas.
(function () {
  var wall = document.getElementById("wall");
  if (!wall || !window.EventSource) return;

  var failures = 0;

  function refresh() {
    fetch("/tiles", { credentials: "same-origin" })
      .then(function (r) {
        // Not an error page — a login page. An authenticating proxy answers an
        // expired session with a redirect, which fetch follows, so what comes
        // back is HTML that is not ours. Reloading hands the browser the
        // redirect as a navigation, which is the only thing that can actually
        // sign back in.
        if (r.redirected || !r.ok) { window.location.reload(); return null; }
        return r.text();
      })
      .then(function (html) { if (html !== null) wall.innerHTML = html; })
      .catch(function () { /* the next event will try again */ });
  }

  function connect() {
    var es = new EventSource("/events");

    es.addEventListener("open", function () { failures = 0; });
    es.addEventListener("tiles", refresh);

    // An EventSource whose session has expired fails quietly and retries
    // forever, leaving a page showing stale green. After a few failures,
    // reload: whatever is wrong, a navigation is what resolves it.
    es.addEventListener("error", function () {
      es.close();
      failures++;
      if (failures >= 3) { window.location.reload(); return; }
      setTimeout(connect, Math.min(1000 * failures, 5000));
    });
  }

  connect();
})();
