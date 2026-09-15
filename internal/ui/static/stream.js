/*
 * Live log viewer.
 *
 * Its own file rather than a fourth behaviour in app.js, for the reason
 * app.js states about itself: that file holds exactly the three repairs an
 * HTMX swap needs, and growing it is the signal that logic has started
 * leaking back to the client. This is a different thing -- one page's
 * behaviour -- and keeping it separate also keeps it off the other pages,
 * which have no stream to read.
 *
 * The connection is a plain EventSource against a same-origin path. That is
 * only possible because the control plane accepts a session cookie: an
 * EventSource cannot set an Authorization header, and before this phase the
 * API accepted nothing else, which is why the previous UI's log viewer
 * could never have worked regardless of what else was fixed.
 */
(function () {
  "use strict";

  // A hard ceiling on retained lines. A job that logs for an hour would
  // otherwise grow the DOM until the tab this is being watched in becomes
  // unusable -- and the reader who most needs this page is the one who
  // leaves it open longest.
  var MAX_LINES = 2000;

  function setStatus(node, text) {
    if (node) {
      node.textContent = text;
    }
  }

  // The class suffix each published status maps to.
  //
  // A lookup table rather than the status interpolated straight into a
  // class name, because event.data is remote input and
  // "stream-line-" + arbitrary text is a class-injection seam: a publisher
  // that ever sent "ok x" would write two classes, and one that sent a
  // known utility class would borrow its styling. Only these words can
  // reach the DOM.
  //
  // internal/adapters/legacy/stdout_parser.go folds "unreachable" into
  // "failed" and "skipping" into "ok" before publishing, so the four live
  // statuses are the first four here. The rest are carried because
  // cmd/demo publishes "task.completed" as a status and a future adapter
  // may stop folding, and a line whose status is not understood must
  // still render.
  var LINE_KINDS = {
    started: "started",
    ok: "ok",
    changed: "changed",
    failed: "failed",
    skipped: "skipped",
    skipping: "skipped",
    unreachable: "failed",
    "task.completed": "task"
  };

  // TIME_RE pulls HH:MM:SS out of an RFC 3339 timestamp.
  //
  // A regex over the string rather than Date parsing, because the only
  // question being asked is what the publisher already wrote down. Passing
  // it through Date would reinterpret it in the reader's own zone, so two
  // people reading the same failure would quote different times to each
  // other, and an unparseable value would render as "Invalid Date" instead
  // of simply being left out.
  var TIME_RE = /T(\d{2}:\d{2}:\d{2})/;

  // formatEvent turns one published wire.JobEvent into the line a reader
  // sees, and the class that colours it.
  //
  // Nothing here decides what a line MEANS. The status is a field the
  // adapter that ran the task already set, so this reads a classification
  // rather than inventing one, which is the whole difference between it
  // and colouring log text by regex in the browser.
  function formatEvent(raw) {
    var evt;
    try {
      evt = JSON.parse(raw);
    } catch (err) {
      // Not a DTO. Render it verbatim rather than dropping it: an
      // unparseable line is still something an operator needs to see, and
      // swallowing it would make a publisher change look like an outage.
      return { text: raw, kind: "" };
    }
    if (!evt || typeof evt !== "object") {
      return { text: raw, kind: "" };
    }

    var status = typeof evt.status === "string" ? evt.status : "";
    var parts = [];

    var stamp = typeof evt.timestamp === "string" ? TIME_RE.exec(evt.timestamp) : null;
    if (stamp) {
      parts.push(stamp[1]);
    }
    if (status) {
      // The status word is in the text, never only in the colour
      // (WCAG SC 1.4.1). It is also what the filter box searches, so
      // "failed" in the filter finds the failures.
      parts.push(status.toUpperCase());
    }
    if (evt.host) {
      parts.push(String(evt.host));
    }
    if (evt.task) {
      parts.push(String(evt.task));
    }

    var message = evt.event_data && typeof evt.event_data.message === "string"
      ? evt.event_data.message
      : "";
    if (message) {
      parts.push(message);
    }

    // An event carrying nothing renderable still gets a line, showing what
    // arrived. A blank row would read as a gap in the log rather than as a
    // message this viewer did not understand.
    var text = parts.length > 0 ? parts.join("  ") : raw;
    return { text: text, kind: LINE_KINDS[status] || "" };
  }

  function init(output) {
    var url = output.getAttribute("data-stream-url");
    if (!url || typeof window.EventSource !== "function") {
      setStatus(document.getElementById("log-status"), "Live output is not available in this browser.");
      return;
    }

    var status = document.getElementById("log-status");
    var filter = document.querySelector("[data-log-filter]");
    var follow = document.querySelector("[data-log-follow]");

    // Honour the reader's motion preference. Smooth scrolling on a log that
    // appends several times a second is exactly the kind of continuous
    // movement SC 2.3.3 is about.
    var smooth = !(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);

    function matchesFilter(text) {
      var needle = filter && filter.value ? filter.value.toLowerCase() : "";
      return !needle || text.toLowerCase().indexOf(needle) !== -1;
    }

    function applyFilter() {
      var lines = output.querySelectorAll(".stream-line");
      Array.prototype.forEach.call(lines, function (line) {
        // hidden rather than display:none via a class, so the filtered-out
        // lines are removed from the accessibility tree too rather than
        // merely being invisible.
        line.hidden = !matchesFilter(line.textContent);
      });
    }

    function append(text, kind) {
      var line = document.createElement("div");
      line.className = "stream-line" + (kind ? " stream-line-" + kind : "");
      // textContent, never innerHTML. Log output is remote data and this is
      // the one place it reaches the document.
      line.textContent = text;
      line.hidden = !matchesFilter(text);
      output.appendChild(line);

      while (output.childElementCount > MAX_LINES) {
        output.removeChild(output.firstElementChild);
      }

      if (follow && follow.checked) {
        output.scrollTo({ top: output.scrollHeight, behavior: smooth ? "smooth" : "auto" });
      }
    }

    var source = new EventSource(url);

    source.addEventListener("init", function (event) {
      setStatus(status, "Connected.");
      append(event.data, "meta");
    });

    source.onmessage = function (event) {
      var line = formatEvent(event.data);
      append(line.text, line.kind);
    };

    source.onopen = function () {
      setStatus(status, "Connected.");
    };

    source.onerror = function () {
      // EventSource reconnects on its own, so this is a state to report
      // rather than an error to act on. Saying "reconnecting" when the
      // browser is in fact reconnecting is the honest message; saying
      // "failed" would send someone to reload a page that is repairing
      // itself.
      if (source.readyState === EventSource.CLOSED) {
        setStatus(status, "Disconnected. Reload to reconnect.");
      } else {
        setStatus(status, "Reconnecting…");
      }
    };

    if (filter) {
      filter.addEventListener("input", applyFilter);
    }

    // Closing the connection when the page goes away stops the server
    // holding a subscription for a reader who has left.
    window.addEventListener("pagehide", function () {
      source.close();
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    var output = document.getElementById("log-output");
    if (output) {
      init(output);
    }
  });
})();
