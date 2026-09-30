// Графики дашборда. Данные — из <script type="application/json" id="chart-data">,
// цвета — из CSS-токенов «лабораторного журнала»: чернила, акцент (переключаемый) для текущих недель,
// синие чернила для lead time.
(function () {
  "use strict";
  var el = document.getElementById("chart-data");
  if (!el || typeof Chart === "undefined") return;
  var data = JSON.parse(el.textContent);

  var css = getComputedStyle(document.documentElement);
  var tok = function (name) { return css.getPropertyValue(name).trim(); };
  var ink = tok("--ink"), ink2 = tok("--ink-2"), muted = tok("--ink-3"), grid = tok("--grid"), axis = tok("--axis");
  var series = tok("--series-1"), series2 = tok("--series-2"), surface = tok("--surface");

  Chart.defaults.font.family = '"JetBrains Mono", ui-monospace, monospace';
  Chart.defaults.font.size = 10.5;
  Chart.defaults.color = muted;
  Chart.defaults.animation = false;

  var tooltip = {
    backgroundColor: surface, titleColor: ink, bodyColor: ink2, borderColor: ink, borderWidth: 1,
    cornerRadius: 0, padding: 10, displayColors: false,
    titleFont: { family: '"Golos Text", system-ui, sans-serif', size: 12, weight: "600" },
    bodyFont: { family: '"JetBrains Mono", monospace', size: 11.5 },
  };
  var scales = function () {
    return {
      x: { grid: { display: false }, border: { color: axis }, ticks: { maxRotation: 0, autoSkipPadding: 16 } },
      y: { beginAtZero: true, grid: { color: grid }, border: { display: false }, ticks: { precision: 0, maxTicksLimit: 4 } },
    };
  };

  var activity = document.getElementById("chart-activity");
  if (activity) {
    var commits = data.commits || [];
    // Последние недели — «текущие данные» — выделяем киноварью (как в макете: 4 из 52).
    var recent = Math.max(1, Math.round(commits.length / 13));
    var colors = function () {
      var accent = getComputedStyle(document.documentElement).getPropertyValue("--series-now").trim();
      return commits.map(function (_, i) { return i >= commits.length - recent ? accent : series; });
    };
    var activityChart = new Chart(activity, {
      type: "bar",
      data: { labels: data.weeks || [], datasets: [{ label: "Коммиты", data: commits, backgroundColor: colors(),
        borderRadius: 0, categoryPercentage: 0.9, barPercentage: 0.9, maxBarThickness: 32 }] },
      options: { maintainAspectRatio: false, plugins: { legend: { display: false }, tooltip: tooltip }, scales: scales() },
    });
    // Смена цвета интерфейса (theme.js) — перекрашиваем текущие недели без перезагрузки.
    document.addEventListener("tp:accent", function () {
      activityChart.data.datasets[0].backgroundColor = colors();
      activityChart.update();
    });
  }

  var lead = document.getElementById("chart-lead");
  if (lead) {
    new Chart(lead, {
      type: "line",
      data: { labels: data.weeks || [], datasets: [{ label: "Lead time, ч", data: data.lead || [],
        borderColor: series2, backgroundColor: "rgba(36, 73, 122, 0.08)", fill: "origin", borderWidth: 1.75,
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
