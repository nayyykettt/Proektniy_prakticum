// Графики дашборда. Данные — из <script type="application/json" id="chart-data">,
// цвета — из CSS-токенов стиля Т1: светло-голубые столбцы, фирменный голубой для текущих недель и линии lead time.
(function () {
  "use strict";
  var el = document.getElementById("chart-data");
  if (!el || typeof Chart === "undefined") return;
  var data = JSON.parse(el.textContent);

  var css = getComputedStyle(document.documentElement);
  var tok = function (name) { return css.getPropertyValue(name).trim(); };
  var ink = tok("--ink"), muted = tok("--muted"), grid = tok("--grid"), axis = tok("--axis");
  var series = tok("--series-1"), seriesNow = tok("--series-now"), series2 = tok("--series-2");
  var font = '"Onest", Inter, system-ui, sans-serif';

  Chart.defaults.font.family = font;
  Chart.defaults.font.size = 13;
  Chart.defaults.color = muted;
  Chart.defaults.animation = false;

  var tooltip = {
    backgroundColor: "#fff", titleColor: ink, bodyColor: muted, borderColor: axis, borderWidth: 1,
    cornerRadius: 12, padding: 12, displayColors: false,
    titleFont: { family: font, size: 14, weight: "500" },
    bodyFont: { family: font, size: 13 },
  };
  var scales = function () {
    return {
      x: { grid: { display: false }, border: { color: axis }, ticks: { maxRotation: 0, autoSkipPadding: 16 } },
      y: { beginAtZero: true, grid: { color: grid }, border: { display: false }, ticks: { precision: 0, maxTicksLimit: 4, padding: 8 } },
    };
  };

  var activity = document.getElementById("chart-activity");
  if (activity) {
    var commits = data.commits || [];
    // Последние недели — «текущие данные» — выделяем фирменным голубым.
    var recent = Math.max(1, Math.round(commits.length / 13));
    var colors = commits.map(function (_, i) { return i >= commits.length - recent ? seriesNow : series; });
    new Chart(activity, {
      type: "bar",
      data: { labels: data.weeks || [], datasets: [{ label: "Коммиты", data: commits, backgroundColor: colors,
        borderRadius: { topLeft: 4, topRight: 4 }, borderSkipped: "bottom", categoryPercentage: 0.9, barPercentage: 0.9, maxBarThickness: 32 }] },
      options: { maintainAspectRatio: false, plugins: { legend: { display: false }, tooltip: tooltip }, scales: scales() },
    });
  }

  var lead = document.getElementById("chart-lead");
  if (lead) {
    new Chart(lead, {
      type: "line",
      data: { labels: data.weeks || [], datasets: [{ label: "Lead time, ч", data: data.lead || [],
        borderColor: series2, backgroundColor: "rgba(29, 175, 247, 0.10)", fill: "origin", borderWidth: 3,
        borderJoinStyle: "round", borderCapStyle: "round",
        pointRadius: 0, pointHoverRadius: 4, pointBackgroundColor: series2, spanGaps: false, tension: 0 }] },
      options: {
        maintainAspectRatio: false,
        interaction: { mode: "index", intersect: false },
        plugins: { legend: { display: false }, tooltip: Object.assign({}, tooltip, {
          callbacks: { label: function (c) { return c.parsed.y == null ? "нет влитых MR" : "медиана: " + c.parsed.y.toFixed(1) + " ч"; } },
        }) },
        scales: scales(),
      },
    });
  }
})();
