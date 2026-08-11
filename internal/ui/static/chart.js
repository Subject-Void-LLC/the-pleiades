/*
 * Chart bootstrap.
 *
 * This file exists as a file, rather than as an inline <script>, because
 * the content security policy this UI ships under carries no
 * 'unsafe-inline'. That is not a constraint being worked around: an inline
 * script is the primary vehicle for reflected XSS, and a policy that
 * forbids them turns a whole class of injection into a browser-refused
 * request. So the chart reads its data source from a data attribute and
 * fetches it, which also means the rendering path is the same one a real
 * client would use rather than a data island only this page understands.
 *
 * The endpoint returns domain JSON, never an ECharts option document.
 * Coupling a server endpoint to a charting library's schema is a seam that
 * can never be changed afterwards -- swapping the library would become an
 * API break. The same JSON also drives the table rendered beside every
 * chart, which is that chart's real text alternative.
 */
(function () {
  "use strict";

  function readTheme() {
    var explicit = document.documentElement.getAttribute("data-theme");
    if (explicit === "dark" || explicit === "light") {
      return explicit;
    }
    return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches
      ? "dark"
      : "light";
  }

  // Read the palette from the stylesheet rather than restating it here.
  // A second copy of the colours in JavaScript is a second thing to
  // update, and the copy that gets forgotten is always the one nobody
  // looks at in both themes.
  function token(name, fallback) {
    var value = getComputedStyle(document.documentElement).getPropertyValue(name);
    return (value || "").trim() || fallback;
  }

  function render(el, data) {
    if (!window.echarts) {
      return;
    }
    var chart = window.echarts.init(el, null, { renderer: "canvas" });
    var fg = token("--fg", "#000000");
    var border = token("--border", "#000000");

    chart.setOption({
      // No animation: this design's own timing function is linear and its
      // whole aesthetic is unpolished. It also means a reduced-motion
      // user gets the same chart as everyone else rather than a
      // separately-maintained still version.
      animation: false,
      textStyle: { fontFamily: "IBM Plex Mono, ui-monospace, monospace", color: fg },
      grid: { left: 48, right: 16, top: 24, bottom: 32, borderColor: border },
      xAxis: {
        type: "category",
        data: data.buckets.map(function (b) { return b.label; }),
        axisLine: { lineStyle: { color: border, width: 2 } },
        axisLabel: { color: fg },
      },
      yAxis: {
        type: "value",
        axisLine: { lineStyle: { color: border, width: 2 } },
        axisLabel: { color: fg },
        splitLine: { lineStyle: { color: border, opacity: 0.2 } },
      },
      series: [
        {
          type: "bar",
          data: data.buckets.map(function (b) {
            // Colour comes from the bucket's own status, and the label
            // beneath every bar repeats it in words. Nothing here is
            // encoded in colour alone.
            return { value: b.count, itemStyle: { color: b.color || token("--fill-neutral", "#d4d4d4"), borderColor: border, borderWidth: 2 } };
          }),
        },
      ],
    });

    window.addEventListener("resize", function () { chart.resize(); });
  }

  document.addEventListener("DOMContentLoaded", function () {
    var nodes = document.querySelectorAll("[data-chart-url]");
    Array.prototype.forEach.call(nodes, function (el) {
      fetch(el.getAttribute("data-chart-url"), { credentials: "same-origin" })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (data) {
          if (data && data.buckets) {
            render(el, data);
          }
          // A failed fetch renders nothing here on purpose. The table
          // beside the chart is server-rendered from the same figures and
          // is already on the page, so the reader still has the data --
          // which is the point of having a text equivalent rather than an
          // alt attribute.
        })
        .catch(function () {});
    });

    // Re-render on theme change so the chart's colours follow the page.
    if (window.matchMedia) {
      var mq = window.matchMedia("(prefers-color-scheme: dark)");
      var onChange = function () {
        readTheme();
        document.querySelectorAll("[data-chart-url]").forEach(function (el) {
          if (window.echarts) {
            var existing = window.echarts.getInstanceByDom(el);
            if (existing) { existing.dispose(); }
          }
        });
        location.reload();
      };
      if (mq.addEventListener) { mq.addEventListener("change", onChange); }
    }
  });
})();
