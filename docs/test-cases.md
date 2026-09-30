# Тест-кейсы TeamPulse

Автотесты запускаются командой `make test` (`go vet` + `go test -race ./...`), интеграционный — `make test-integration` (нужен интернет). Обозначения типа: **auto** — есть автотест, **manual** — проверяется вручную. Формат ссылки: `файл::Тест`. Пути — от корня репозитория.

Связанные документы: [MVP и истории](mvp-and-user-stories.md) · [backlog](backlog.md) · [безопасность](security.md).

## 1. Сбор данных и Git

| ID | Область | Предусловия | Шаги | Ожидаемый результат | Тип | Автотест |
|---|---|---|---|---|---|---|
| TC-01 | Парсинг Git | Репозиторий с коммитами разных авторов | Синхронизировать проект | Коммиты, авторы, добавленные/удалённые строки сохранены; merge-коммиты не считаются | auto | internal/gitparse/gitparse_test.go::TestParseIncremental |
| TC-02 | Инкрементальная синхронизация | Проект синхронизирован; в репозиторий добавлен новый коммит | Запустить синхронизацию повторно | Обработан только новый коммит, дублей нет | auto | internal/gitparse/gitparse_test.go::TestParseIncremental |
| TC-03 | Классификация файлов | Коммит меняет vendor/, lock, сгенерированные и тестовые файлы | Синхронизировать | vendor/lock/generated не входят в размер; тестовые файлы помечены как тесты | auto | internal/gitparse/gitparse_test.go::TestClassifier |
| TC-04 | Пустой репозиторий | Репозиторий без коммитов | Создать проект, синхронизировать | Ошибки нет, 0 новых коммитов, есть предупреждение | auto | internal/app/app_test.go::TestSyncFailuresAndLock |
| TC-05 | Неверная ветка | Ветки `nope` нет | Синхронизировать | Статус `failed`, понятное сообщение | auto | internal/app/app_test.go::TestSyncFailuresAndLock; internal/gitparse/gitparse_test.go::TestResolveUnknownBranch |
| TC-06 | Плохие даты и лимиты | Коммиты с датой до 2000 г. или в будущем, превышен `MAX_COMMITS` | Синхронизировать | Берётся дата коммиттера; если и она некорректна — коммит пропускается, число пропущенных выводится в предупреждении; при лимите загружаются самые новые коммиты, остальные догружаются при следующих синхронизациях | auto | internal/gitparse/gitparse_test.go::TestParseLimitsAndBadDates |
| TC-07 | Валидация URL | — | Создать проект с URL: пустое имя, `/etc`, `https://127.0.0.1/…`, `ftp://…`, ветка `a..b`, провайдер `jira` | Везде ошибка валидации, проект не создан | auto | internal/app/app_test.go::TestValidation |
| TC-08 | SSRF | `ALLOW_LOCAL_REPOS=false` | Создать проект с URL на localhost, приватную сеть, link-local | Отклонено | auto | internal/secure/secure_test.go::TestValidateRemoteURL |
| TC-09 | Повторный запуск sync | Синхронизация идёт | Запустить ещё одну | Отказ (`ErrSyncActive`) | auto | internal/app/app_test.go::TestSyncFailuresAndLock |
| TC-10 | Реальный репозиторий | Интернет | Клонировать публичный репозиторий, синхронизировать, посчитать метрики | Данные и метрики непустые, значения правдоподобны | auto (integration) | internal/app/integration_test.go::TestRealRepository |
| TC-11 | Сквозной сценарий | Тестовый репозиторий | Создать проект, sync, analyze, отчёт | Весь конвейер работает | auto | internal/app/app_test.go::TestEndToEnd |

## 2. Провайдер Т1 Код

| ID | Область | Предусловия | Шаги | Ожидаемый результат | Тип | Автотест |
|---|---|---|---|---|---|---|
| TC-12 | MR и ревью | Mock GitLab API v4 | Загрузить MR (пагинация), notes/approvals, размер diffs | MR, ревью и размеры получены полностью | auto | internal/provider/t1code_test.go::TestFetchMergeRequests |
| TC-13 | Задачи и назначение | Mock API | Загрузить задачи, назначить исполнителя | Задачи получены, назначение выполнено | auto | internal/provider/t1code_test.go::TestFetchIssuesAndAssign |
| TC-14 | 401 | Неверный токен | Синхронизировать | Ошибка авторизации, понятное сообщение | auto | internal/provider/t1code_test.go::TestUnauthorized |
| TC-15 | 429/5xx | Сервер отвечает 429/5xx | Синхронизировать | До 3 повторов с backoff (учитывается `Retry-After`); при стойкой недоступности — понятная ошибка, статус проекта `partial`, данные Git сохранены | auto | internal/provider/t1code_test.go::TestFetchMergeRequests (429), internal/provider/t1code_test.go::TestServerDown (502), internal/app/app_test.go::TestEndToEnd (partial) |
| TC-16 | Частичная синхронизация | Git доступен, провайдер недоступен | Синхронизировать | Git-данные сохранены, предупреждение о провайдере, метрики Git считаются | auto | internal/app/app_test.go::TestEndToEnd |

## 3. Метрики, тренды, рекомендации

| ID | Область | Предусловия | Шаги | Ожидаемый результат | Тип | Автотест |
|---|---|---|---|---|---|---|
| TC-17 | Расчёт метрик | Набор коммитов/MR с известным результатом | Вычислить Summary | Значения совпадают с эталоном (коммиты, активные дни, lead time, размеры и т.д.) | auto | internal/metrics/metrics_test.go::TestComputeSummary |
| TC-18 | Bus factor / владение | Файлы с разными владельцами | Вычислить | Bus factor и доли владения корректны | auto | internal/metrics/metrics_test.go::TestBusFactorAndOwnership |
| TC-19 | Фильтр команды и авторов | Команда из части авторов | Посчитать с фильтром | Учтены только участники | auto | internal/metrics/metrics_test.go::TestTeamFilterAndAuthors |
| TC-20 | Пустые данные | Нет активности | Посчитать | Нули/отсутствие значений без паники и деления на 0 | auto | internal/metrics/metrics_test.go::TestEmptyData |
| TC-21 | Боты | Коммиты dependabot/renovate/[bot] | Посчитать | Боты исключены из метрик | auto | internal/metrics/metrics_test.go::TestWithoutBots |
| TC-22 | Качество сообщений | Сообщения «fix», «wip», Conventional Commits | Проверить | Хорошие/плохие определены верно | auto | internal/metrics/metrics_test.go::TestGoodMessage |
| TC-23 | Перцентили и медиана | Массив значений | Вычислить p50/p85 | Значения верны | auto | internal/metrics/metrics_test.go::TestPercentile |
| TC-24 | Тренды | Два периода | Сравнить | Дельта и вердикт учитывают направление метрики | auto | internal/analysis/analysis_test.go::TestTrends |
| TC-25 | Рекомендации | Снимок с bus factor = 1, долгим lead time, большими MR | Построить рекомендации | Есть рекомендации нужного уровня со ссылкой на метрику; при отсутствии активности — «Нет активности» | auto | internal/analysis/analysis_test.go::TestRecommend |

## 4. Zerocracy

| ID | Область | Предусловия | Шаги | Ожидаемый результат | Тип | Автотест |
|---|---|---|---|---|---|---|
| TC-26 | Начисление баллов | Политика по умолчанию, MR/ревью/задачи | Evaluate | Баллы и breakdown по политике, штрафы применены | auto | internal/zerocracy/zerocracy_test.go::TestEvaluate |
| TC-27 | Воспроизводимость журнала | Те же входные данные | Evaluate дважды | Журнал одинаков | auto | internal/zerocracy/zerocracy_test.go::TestEvaluate |
| TC-28 | Подбор исполнителей | Открытые задачи, разная загрузка | Suggest | Кандидаты отсортированы по счёту, есть причина | auto | internal/zerocracy/zerocracy_test.go::TestSuggest |
| TC-29 | Загрузка политики | JSON с частичными полями; битый JSON | LoadPolicy | Пропущенные поля — по умолчанию; битый файл — ошибка | auto | internal/zerocracy/zerocracy_test.go::TestLoadPolicy |

## 5. Отчёты и API

| ID | Область | Предусловия | Шаги | Ожидаемый результат | Тип | Автотест |
|---|---|---|---|---|---|---|
| TC-30 | Форматы отчёта | Проект с данными | Сформировать HTML, MD, CSV, JSON | Все форматы непустые, корректный MIME | auto | internal/report/report_test.go::TestRenderAllFormats |
| TC-31 | Форматирование | Числа, часы, проценты | Отрендерить | Единицы и округления корректны | auto | internal/report/report_test.go::TestFormatting |
| TC-32 | Хранение отчётов | Созданный отчёт | Получить список и содержимое | Отчёт сохранён и читается | auto | internal/storage/storage_test.go::TestTeamsAndReports |
| TC-33 | Скачивание | Есть отчёт | Открыть `/reports/{id}?format=csv` | Файл CSV скачивается | manual | — (частично: internal/server/server_test.go::TestAPIFlow) |
| TC-34 | REST API | API-токен | CRUD проектов, sync, метрики, тренды, рекомендации, отчёты | Коды и JSON соответствуют ожиданиям | auto | internal/server/server_test.go::TestAPIFlow |

## 6. Аутентификация, права, безопасность

| ID | Область | Предусловия | Шаги | Ожидаемый результат | Тип | Автотест |
|---|---|---|---|---|---|---|
| TC-35 | Вход и роли | Заданы пароли admin и viewer | Войти обеими ролями | viewer видит данные, но не действия admin (403) | auto | internal/server/server_test.go::TestAuthAndRoles |
| TC-36 | Без авторизации | Пароли заданы | Открыть `/` без входа | Редирект на `/login`; API без токена — 401 | auto | internal/server/server_test.go::TestAuthAndRoles |
| TC-37 | CSRF | Сессия admin | POST без токена csrf | Отклонено | auto | internal/server/server_test.go::TestAuthAndRoles |
| TC-38 | Open redirect | — | Вход с `next=//evil.example` | Редирект только на локальный путь | auto | internal/server/server_test.go::TestAuthAndRoles |
| TC-39 | Сессия и шифрование токенов | — | Создать/проверить сессию; зашифровать и расшифровать токен провайдера | Подпись проверяется, подделка отвергается, round-trip успешен | auto | internal/secure/secure_test.go::TestSession; ::TestEncryptRoundTrip |
| TC-40 | Заголовки безопасности | Запущен сервис | Проверить ответ (CSP, X-Frame-Options, nosniff, Referrer-Policy) | Заголовки присутствуют | auto | internal/server/server_test.go::TestAuthAndRoles |
| TC-41 | Конфигурация | Не заданы пароли/секрет; неверные значения | Загрузить конфиг | Значения по умолчанию, секрет генерируется; неверные значения отклонены | auto | internal/config/config_test.go::TestLoadDefaultsAndSecret; ::TestLoadValidation |

## 7. Период, команды, авторы

| ID | Область | Предусловия | Шаги | Ожидаемый результат | Тип | Автотест |
|---|---|---|---|---|---|---|
| TC-42 | Период from > to | — | Задать `from=2026-09-10&to=2026-09-01` | Ошибка валидации | auto | internal/app/app_test.go::TestParsePeriod; ::TestSyncFailuresAndLock |
| TC-43 | Неверный формат даты | — | `from=10.09.2026` | Ошибка формата | auto | internal/app/app_test.go::TestParsePeriod |
| TC-44 | Период по умолчанию | — | Пустые from/to | 30 дней до сегодня | auto | internal/app/app_test.go::TestParsePeriod |
| TC-45 | Команды | Проект с авторами | Создать, изменить, удалить команду; фильтр на дашборде | Метрики считаются по составу команды | auto | internal/storage/storage_test.go::TestTeamsAndReports; internal/metrics/metrics_test.go::TestTeamFilterAndAuthors |
| TC-46 | Объединение авторов | Два e-mail одного человека | Объединить в настройках | Один автор во всех метриках, связки Git/провайдер сохранены | auto | internal/storage/storage_test.go::TestAuthorIdentityLinksGitAndProvider |
| TC-47 | CRUD проекта | — | Создать, прочитать, обновить, удалить | Каскадное удаление данных | auto | internal/storage/storage_test.go::TestProjectCRUD |

## 8. Планировщик, UI, эксплуатация

| ID | Область | Предусловия | Шаги | Ожидаемый результат | Тип | Автотест |
|---|---|---|---|---|---|---|
| TC-48 | Планировщик | `SYNC_INTERVAL`, `REPORT_INTERVAL` | Выполнить один цикл | Проекты синхронизированы, отчёты созданы | auto | internal/scheduler/scheduler_test.go::TestRunOnceSyncsAndReports |
| TC-49 | Рендер страниц | Проект с данными | Открыть список, дашборд, автора, отчёты, настройки, логин, страницу ошибки | Все страницы 200 без ошибок шаблонов | auto | internal/server/server_test.go::TestPagesRender |
| TC-50 | Адаптивность | Данные загружены | Сузить окно до 360–400 px | Колонки в одну, таблицы прокручиваются, нет горизонтального скролла страницы | manual | — |
| TC-51 | Тёмная тема | ОС в тёмной теме | Открыть дашборд | Читаемые графики, тепловая карта, бейджи | manual | — |
| TC-52 | Графики | Данные с провайдером | Открыть дашборд | Отрисованы «Коммиты по неделям», «Lead time», «Баллы» | manual | — |
| TC-53 | Пустое состояние | Проект без синхронизации | Открыть дашборд | Карточка «Данные ещё не загружены», нет ошибок | manual | — |
| TC-54 | Healthcheck | Сервис запущен | `teampulse healthcheck`; `GET /healthz` | Код 0 / HTTP 200 | manual | — |
| TC-55 | Docker | `.env` заполнен | `make docker-up` | Контейнер стартует, healthcheck зелёный, данные в volume | manual | — |

## Чек-лист регрессии (спринт 5)

- [ ] `make lint` и `make test` зелёные (включая `-race`).
- [ ] `make test-integration` проходит на реальном репозитории.
- [ ] Свежая установка: пустая БД, миграции применяются, вход admin и viewer работает.
- [ ] Добавление проекта: валидные и невалидные URL (TC-07, TC-08).
- [ ] Первая и повторная синхронизации; пустой репозиторий; неверная ветка (TC-01…TC-06).
- [ ] Провайдер: корректный токен, неверный токен, недоступный сервер (TC-14…TC-16).
- [ ] Дашборд: пресеты 7/14/30/90, произвольный период, ошибки периода, фильтр команды.
- [ ] Значения KPI совпадают с таблицей «Все метрики»; дельты и вердикты корректны.
- [ ] «Мой вклад» открывается для каждого автора, баллы совпадают с таблицей Zerocracy.
- [ ] Отчёты: создание вручную и по расписанию; скачивание HTML, MD, CSV, JSON.
- [ ] Права: viewer не видит кнопок admin и получает 403 на POST; CSRF и open redirect (TC-35…TC-38).
- [ ] UI: светлая/тёмная тема, ширина 360 px, клавиатурная навигация, пустые состояния.
- [ ] Объединение авторов и команды: метрики пересчитываются корректно.
- [ ] Docker-образ собирается, контейнер здоров, данные сохраняются после перезапуска.
- [ ] README соответствует актуальным командам и переменным окружения.
