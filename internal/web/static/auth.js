// Проверка формы регистрации в браузере: логин, длина пароля и совпадение повтора.
// Сервер проверяет всё то же самое повторно — это лишь быстрая подсказка.
(function () {
  "use strict";
  var form = document.getElementById("register-form");
  if (!form) return;
  var login = form.elements.login;
  var pass = form.elements.password;
  var confirm = form.elements.password_confirm;
  var loginRe = /^[A-Za-z0-9._-]+$/;

  function hint(input) { return document.getElementById(input.getAttribute("aria-describedby")); }

  function mark(input, msg, okMsg) {
    var h = hint(input);
    input.setCustomValidity(msg || "");
    input.setAttribute("aria-invalid", msg ? "true" : "false");
    if (!h) return;
    if (!h.dataset.base) h.dataset.base = h.textContent;
    h.textContent = msg || okMsg || h.dataset.base;
    h.classList.toggle("err", !!msg);
    h.classList.toggle("ok", !msg && !!okMsg);
  }

  function checkLogin(strict) {
    var v = login.value.trim(), min = +login.minLength, max = +login.maxLength;
    if (!v && !strict) return mark(login, "");
    if (v.length < min || v.length > max) return mark(login, "Логин — от " + min + " до " + max + " символов");
    if (!loginRe.test(v)) return mark(login, "Только латинские буквы, цифры и символы . _ -");
    mark(login, "");
  }

  function checkPass(strict) {
    var v = pass.value, min = +pass.minLength;
    if (!v && !strict) return mark(pass, "");
    if (v.length < min) return mark(pass, "Ещё " + (min - v.length) + " симв. — минимум " + min);
    if (v.toLowerCase() === login.value.trim().toLowerCase()) return mark(pass, "Пароль не должен совпадать с логином");
    mark(pass, "", "Длина пароля подходит");
  }

  function checkConfirm(strict) {
    if (!confirm.value && !strict) return mark(confirm, "");
    if (confirm.value !== pass.value) return mark(confirm, "Пароли не совпадают");
    mark(confirm, "", "Пароли совпадают");
  }

  login.addEventListener("input", function () { checkLogin(false); });
  pass.addEventListener("input", function () { checkPass(false); if (confirm.value) checkConfirm(false); });
  confirm.addEventListener("input", function () { checkConfirm(false); });

  form.addEventListener("submit", function (e) {
    checkLogin(true); checkPass(true); checkConfirm(true);
    var bad = [login, pass, confirm].filter(function (i) { return !i.checkValidity(); });
    if (bad.length) {
      e.preventDefault();
      bad[0].focus();
    }
  });
})();
