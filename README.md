# TeamPulse — автоматическая оценка эффективности команд разработки

TeamPulse сам собирает данные из Git-репозиториев и платформы **Т1 Код (Сфера.Код)**, считает объективные метрики
производительности команды и каждого разработчика, показывает тренды и рекомендации, начисляет баллы
по принципам **Zerocracy** и формирует отчёты. Ручной работы не требуется: синхронизация и еженедельные отчёты
выполняются по расписанию.

Учебный кейс ТОП-ИТ «Создание системы автоматической оценки эффективности команд разработки». Реализовано на **Go**.

## Возможности

| Что | Как |
|---|---|
| Сбор данных из Git | клонирование по https (в т.ч. приватные репозитории с токеном), инкрементальная загрузка новых коммитов, статистика по файлам |
| Интеграция с Т1 Код | merge request'ы, ревью (комментарии и approve), размер diff, задачи; автоназначение задач |
| 22 метрики | поток (lead time, время до ревью, throughput, cycle time), качество (размер коммитов/MR, rework, тесты, сообщения, покрытие ревью), команда (bus factor, переработки, ревью) — [docs/metrics.md](docs/metrics.md) |
| Объективность | исключаются merge-коммиты, vendor/lock/сгенерированные файлы и боты; медианы вместо средних; сравнение с прошлым периодом |
| Тренды и рекомендации | изменение каждой метрики к предыдущему периоду той же длины; правила с понятными советами |
| Zerocracy | баллы за результат по публичной политике, журнал начислений, подбор исполнителей задач — [docs/zerocracy.md](docs/zerocracy.md) |
| Дашборды | KPI, графики по неделям, тепловая карта работы, владение кодом, горячие точки, фильтр по периоду и команде, страница «Мой вклад» |
| Отчёты | HTML (печать в PDF), Markdown, CSV (Excel), JSON; история; автоматический еженедельный отчёт |
| Безопасность | роли руководитель/разработчик, шифрование токенов, CSRF, CSP, защита от SSRF — [docs/security.md](docs/security.md) |

## Быстрый старт

Нужен Go 1.27+ (или Docker).

```bash
make run            # http://localhost:8080, dev-режим без пароля
```

1. Нажмите «Добавить проект», укажите, например, `https://github.com/go-git/go-billy`.
2. Дождитесь синхронизации (страница обновится сама) и выберите период «365 дн».
3. Откройте разработчика в таблице «Вклад разработчиков» — это его страница «Мой вклад».
4. Во вкладке «Отчёты» сформируйте отчёт и скачайте его в нужном формате.

Для Т1 Код в форме проекта выберите платформу «Т1 Код / Сфера.Код», адрес инстанса, ID или путь проекта
(`group/app`) и токен с правами `read_api`, `read_repository`.

### Docker

```bash
cp .env.example .env   # задайте APP_SECRET, API_TOKEN; пользователи регистрируются на /register
docker compose up -d --build
```

Данные (SQLite и клоны репозиториев) хранятся в томе `/data`. Образ — distroless, без shell, запуск от непривилегированного пользователя.

### Публичный деплой

Подойдёт любой хостинг с Docker и постоянным диском:

- **VPS**: `docker compose up -d`, перед сервисом — Caddy/nginx с HTTPS, `COOKIE_SECURE=true`.
- **Render / Fly.io / Railway**: деплой из Dockerfile, подключите постоянный том в `/data`, задайте переменные окружения из `.env.example`.

Проверка живости: `GET /healthz`.

## Командная строка

```bash
teampulse serve                                  # веб, API и планировщик
teampulse sync   [-project ID]                   # синхронизировать один или все проекты
teampulse report -project 1 -from 2026-09-01 -to 2026-09-30 -format md -out report.md
```

## Конфигурация

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `ADDR` | `:8080` | адрес сервера |
| `DATA_DIR` | `./data` | БД и клоны репозиториев |
| `APP_SECRET` | генерируется в `DATA_DIR/secret.key` | ключ шифрования токенов и подписи сессий (≥16 символов) |
| `API_TOKEN` | — | сервисный Bearer-токен для JSON API (видит все проекты) |
| `COOKIE_SECURE` | `false` | `true` при работе по HTTPS |
| `SYNC_INTERVAL` | `6h` | период автосинхронизации (`0` — выкл.) |
| `REPORT_INTERVAL` | `168h` | период автоотчётов (`0` — выкл.) |
| `WORKDAY_START` / `WORKDAY_END` | `9` / `19` | рабочие часы для метрики переработок |
| `ZEROCRACY_POLICY` | — | путь к JSON-политике баллов ([пример](docs/policy.example.json)) |
| `ZEROCRACY_AUTO_ASSIGN` | `false` | назначать задачи в Т1 Код автоматически |
| `EXCLUDE_PATTERNS` | — | доп. исключаемые пути: `docs/,*.md` |
| `MAX_COMMITS` | `20000` | максимум новых коммитов за синхронизацию (остальное догрузится позже) |
| `CLONE_TIMEOUT` | `10m` | таймаут клонирования |
| `ALLOW_LOCAL_REPOS` | `false` | разрешить локальные пути и внутренние адреса (только для разработки) |

## REST API

Авторизация: `Authorization: Bearer $API_TOKEN`. Период — `?from=ГГГГ-ММ-ДД&to=ГГГГ-ММ-ДД` (по умолчанию 30 дней), команда — `&team=ID`.

| Метод и путь | Описание |
|---|---|
| `GET/POST /api/projects`, `GET/PUT/DELETE /api/projects/{id}` | проекты |
| `POST /api/projects/{id}/sync[?wait=true]` | синхронизация (202 — в фоне) |
| `GET /api/projects/{id}/metrics` | снимок метрик: сводка, авторы, недели, тепловая карта, hotspots, владение |
| `GET /api/projects/{id}/trends` · `/recommendations` | тренды и рекомендации |
| `GET /api/projects/{id}/contribution` · `/assignments` | баллы Zerocracy и подбор исполнителей |
| `GET /api/projects/{id}/authors[/{aid}]` · `POST …/authors/merge` | разработчики, «Мой вклад», слияние идентичностей |
| `GET/POST /api/projects/{id}/teams`, `DELETE …/teams/{tid}` | команды |
| `GET/POST /api/projects/{id}/reports`, `GET /api/reports/{rid}?format=html\|md\|csv\|json` | отчёты |
| `GET /api/metrics/definitions` | описания и формулы метрик |

```bash
curl -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"billy","repo_url":"https://github.com/go-git/go-billy"}' localhost:8080/api/projects
curl -X POST -H "Authorization: Bearer $API_TOKEN" "localhost:8080/api/projects/1/sync?wait=true"
curl -H "Authorization: Bearer $API_TOKEN" "localhost:8080/api/projects/1/metrics?from=2026-01-01"
```

Ошибки возвращаются как `{"error": "понятное описание"}` с кодами 400/401/403/404/409.

## Тесты

```bash
make test              # go vet + все тесты с -race
make test-integration  # полный цикл на реальном публичном репозитории (нужен интернет)
make cover
```

Тесты используют детерминированные Git-репозитории, создаваемые во временных каталогах, эмулятор API Т1 Код на
`httptest` и настоящий HTTP-сервер для проверки ролей, CSRF и страниц. Тест-кейсы для QA — [docs/test-cases.md](docs/test-cases.md).

## Структура

```
cmd/teampulse/        точка входа: serve | sync | report | healthcheck
internal/config       настройки из окружения
internal/domain       модели предметной области
internal/storage      SQLite, встроенные миграции
internal/gitparse     клонирование и разбор истории Git
internal/provider     интеграция с Т1 Код (GitLab API v4)
internal/metrics      расчёт метрик
internal/analysis     тренды и рекомендации
internal/zerocracy    баллы и распределение задач
internal/report       отчёты HTML/MD/CSV/JSON
internal/scheduler    автоматическая синхронизация и отчёты
internal/app          прикладной слой (оркестрация)
internal/httpx        авторизация и middleware
internal/api          REST API
internal/web          веб-интерфейс (шаблоны, CSS, Chart.js)
internal/server       сборка HTTP-сервера
docs/                 документация проекта
```

Подробнее: [архитектура](docs/architecture.md) · [MVP и user stories](docs/mvp-and-user-stories.md) ·
[wireframe](docs/wireframe.md) · [бэклог](docs/backlog.md) · [презентация](docs/presentation.md).
