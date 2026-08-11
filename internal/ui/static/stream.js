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
      append(event.data);
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
