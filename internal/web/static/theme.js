// Переключатель цвета интерфейса (голубой / синий / зелёный). Выбор хранится в cookie tp_accent:
// сервер сразу отдаёт страницу с нужным data-accent, поэтому при загрузке нет мигания.
(function () {
  "use strict";
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-accent-pick]");
    if (!btn) return;
    var accent = btn.getAttribute("data-accent-pick");
    document.documentElement.setAttribute("data-accent", accent);
    document.cookie = "tp_accent=" + accent + "; path=/; max-age=31536000; SameSite=Lax";
    document.querySelectorAll("[data-accent-pick]").forEach(function (b) {
      b.setAttribute("aria-pressed", b.getAttribute("data-accent-pick") === accent ? "true" : "false");
    });
    document.dispatchEvent(new CustomEvent("tp:accent", { detail: accent }));
  });
})();
