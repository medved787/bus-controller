# bus-controller

Лёгкий сервис мониторинга доступности сервисов (TCP-порт или HTTP `/health`-эндпоинт) на бэкенде Go с веб-интерфейсом (тёмная тема, живые карточки статусов) и возможностью вручную запустить вебхук (например, AWX/Ansible job) для перезапуска сервиса прямо из UI.

## Возможности

- Периодическая проверка доступности списка сервисов с настраиваемым интервалом и таймаутом. Для каждого сервиса можно выбрать тип проверки: TCP-соединение до `host:port` или HTTP GET-запрос на health-эндпоинт (по умолчанию `/health`).
- Веб-интерфейс с карточками сервисов: статус (`online` / `degraded` / `offline` / `checking…`), время последней проверки и время ответа в мс, текст последней ошибки.
- Кнопка **Restart** на карточке — отправляет POST-запрос на настроенный вебхук AWX (эндпоинт вида `.../github/`, созданный опцией **Enable Webhook** на Job Template), без API-токена, с HMAC-SHA256 подписью тела запроса (`X-Hub-Signature-256`).
- Опциональный reverse-proxy с авторизацией через Keycloak (`oauth2-proxy`), чтобы не открывать панель наружу без логина.

## Структура репозитория

```
.
├── docker-compose.yml          # оркестрация: bus-controller (backend) + frontend (nginx) + oauth2-proxy
├── backend/
│   ├── Dockerfile               # сборка Go-бинарника + рантайм-образ (только API)
│   ├── main.go                  # точка входа, роутинг HTTP (/api/*)
│   ├── config.go                # загрузка и валидация services.json
│   ├── checker.go                # фоновые TCP-проверки, хранилище статусов
│   ├── handlers.go               # HTTP-хендлеры: /api/status, /api/trigger/{id}
│   ├── go.mod                    # модуль bus-controller, Go 1.22
│   └── services.json             # конфиг сервисов и вебхуков (пример)
└── frontend/
    ├── Dockerfile                # сборка образа nginx со статикой
    ├── nginx.conf                # раздача статики + reverse proxy /api/* → bus-controller:8000
    ├── index.html                # разметка страницы
    ├── app.js                     # опрос /api/status, рендер карточек, вызов /api/trigger
    └── style.css                   # тёмная тема оформления
```

Backend и frontend — отдельные сервисы docker-compose и отдельные Docker-образы. Backend отдаёт только JSON API (`/api/status`, `/api/trigger/{id}`) и ничего не знает о UI — это позволяет подключать к нему любые другие frontend'ы, не трогая backend.

## Как это работает

1. При старте `main.go` читает путь к конфигу из переменной окружения `SERVICES_CONFIG` (по умолчанию `services.json`) и парсит список сервисов через `config.go`.
2. `checker.go` запускает горутину, которая с интервалом `check_interval_seconds` параллельно (`sync.WaitGroup`) проверяет каждый сервис одним из двух способов, в зависимости от его `check_type`:
   - `tcp` (по умолчанию) — пытается открыть TCP-соединение до `host:port` с таймаутом `tcp_timeout_seconds`;
   - `http` — выполняет HTTP GET на health-URL (по умолчанию `http://host:port/health`, настраивается через `health_scheme` / `health_path` / `health_url`) с таймаутом `http_timeout_seconds`; ответ с кодом 2xx считается успешным, всё остальное (сетевые ошибки, коды вне диапазона 2xx) — `offline`.

   Для каждой проверки замеряется время выполнения (`response_time_ms`). Если проверка прошла успешно, но заняла больше `slow_threshold_ms` (глобально или per-service), статус — `degraded`, а не `online`. Итоговый статус (`online` / `degraded` / `offline`) вместе со временем ответа и текстом последней ошибки сохраняется в потокобезопасном `StatusStore`.
3. `handlers.go` отдаёт:
   - `GET /api/status` — JSON-массив текущих статусов всех сервисов;
   - `POST /api/trigger/{id}` — находит сервис по `id`, собирает HTTP-запрос по настройкам его `webhook` (URL, метод, заголовки, тело), при наличии `hmac_secret` добавляет подпись HMAC-SHA256 в заголовок `hmac_header` в формате `sha256=<hex>` (совместимо с проверкой подписи AWX-вебхука для GitHub-сервиса), отправляет запрос и возвращает результат клиенту.

   Backend больше не раздаёт статику UI — этим занимается отдельный сервис `frontend` (nginx).
4. Frontend — статический сайт (`index.html`/`app.js`/`style.css`), который отдаёт nginx (см. `frontend/nginx.conf`). Nginx проксирует все запросы `/api/*` на `bus-controller:8000`, поэтому для браузера frontend и API выглядят как один origin и `app.js` ходит по относительным путям (`/api/status`, `/api/trigger/{id}`) без CORS. `app.js` раз в 10 секунд опрашивает `/api/status` и перерисовывает карточки; при клике на **Restart** дергает `/api/trigger/{id}` и показывает toast с результатом.

## Конфигурация (`services.json`)

```json
{
  "check_interval_seconds": 10,
  "tcp_timeout_seconds": 3,
  "http_timeout_seconds": 3,
  "slow_threshold_ms": 1000,
  "services": [
    {
      "id": "svc1",
      "name": "repomanager",
      "host": "127.0.0.1",
      "port": 8080,
      "check_type": "tcp",
      "webhook": {
        "url": "https://awx.example.com/api/v2/job_templates/1/github/",
        "method": "POST",
        "headers": { "Content-Type": "application/json", "X-GitHub-Event": "push" },
        "body": "{}",
        "hmac_secret": "REPLACE_WITH_WEBHOOK_KEY_FROM_AWX_JOB_TEMPLATE_1",
        "hmac_header": "X-Hub-Signature-256"
      }
    },
    {
      "id": "svc-health",
      "name": "api-gateway",
      "host": "10.0.0.20",
      "port": 8443,
      "check_type": "http",
      "health_scheme": "https",
      "health_path": "/health",
      "slow_threshold_ms": 300,
      "webhook": {
        "url": "https://awx.example.com/api/v2/job_templates/3/github/",
        "method": "POST",
        "headers": { "Content-Type": "application/json", "X-GitHub-Event": "push" },
        "body": "{}",
        "hmac_secret": "REPLACE_WITH_WEBHOOK_KEY_FROM_AWX_JOB_TEMPLATE_3",
        "hmac_header": "X-Hub-Signature-256"
      }
    }
  ]
}
```

Поля:

| Поле | Обязательное | Описание |
|---|---|---|
| `check_interval_seconds` | нет (по умолчанию 10) | как часто проверять сервисы |
| `tcp_timeout_seconds` | нет (по умолчанию 3) | таймаут TCP-подключения (для `check_type: "tcp"`) |
| `http_timeout_seconds` | нет (по умолчанию 3) | таймаут HTTP-запроса (для `check_type: "http"`) |
| `slow_threshold_ms` | нет (по умолчанию 0 — отключено) | глобальный порог времени ответа (мс), после которого успешная проверка помечается как `degraded` вместо `online` |
| `services[].id` | да | уникальный идентификатор, используется в URL `/api/trigger/{id}` |
| `services[].name` | нет | отображаемое имя в UI |
| `services[].host` / `port` | да | адрес сервиса; всегда обязателен, даже при `check_type: "http"`, т.к. используется в UI и (если не задан `health_url`) для построения адреса проверки |
| `services[].check_type` | нет (по умолчанию `"tcp"`) | тип проверки доступности: `"tcp"` — открыть TCP-соединение до `host:port`; `"http"` — GET-запрос на health-эндпоинт |
| `services[].health_path` | нет (по умолчанию `"/health"`) | путь health-эндпоинта, используется вместе с `host:port` при `check_type: "http"`, если не задан `health_url` |
| `services[].health_scheme` | нет (по умолчанию `"http"`) | схема (`http`/`https`), используемая при построении health-URL из `host:port` |
| `services[].health_url` | нет | полный URL для HTTP-проверки; если задан, переопределяет URL, построенный из `host:port` + `health_path` |
| `services[].slow_threshold_ms` | нет | override порога для конкретного сервиса: `> 0` — свой порог в мс; `0` (или не задано) — использовать глобальный `slow_threshold_ms`; `< 0` (например `-1`) — отключить degraded-детекцию для этого сервиса |
| `services[].webhook.url` | нет | если пусто — кнопка Restart вернёт ошибку "webhook url not configured" |
| `services[].webhook.method` | нет (по умолчанию `POST`) | HTTP-метод запроса к вебхуку |
| `services[].webhook.headers` | нет | произвольные заголовки запроса |
| `services[].webhook.body` | нет | тело запроса (строка) |
| `services[].webhook.hmac_secret` | нет | если задан — тело подписывается HMAC-SHA256; итоговая подпись отправляется в заголовке `hmac_header` в формате `sha256=<hex>` |
| `services[].webhook.hmac_header` | обязательно, если задан `hmac_secret` | заголовок для подписи (например `X-Hub-Signature-256` для AWX); дефолта в коде нет — если `hmac_secret` задан, а заголовок не указан, подпись уйдёт в заголовок с пустым именем |

Валидация (`config.go`) требует, чтобы у каждого сервиса были `id`, `host` и `port`; иначе приложение не стартует. Если указан `check_type`, он должен быть `"tcp"` или `"http"` — любое другое значение тоже приводит к ошибке при старте.

> **Конфиг перечитывается только при старте процесса.** `main.go` вызывает `LoadConfig` один раз перед запуском сервера — hot-reload не реализован. После правки `services.json` (добавление/удаление сервиса, смена полей) нужно перезапустить процесс (`docker compose restart bus-controller` или заново `go run .`), иначе изменения не появятся ни в `/api/status`, ни на странице. Если работаете через Docker и `services.json` скопирован в образ на этапе сборки (см. «Известные проблемы» ниже), правки на хосте не попадут в контейнер без пересборки (`docker compose up --build`).

### Запуск Job template в AWX через webhook (без API-токена)

Кнопка Restart рассчитана на встроенный механизм **Enable Webhook** в AWX (Job Template → Enable Webhook → Webhook Service: `GitHub`), а не на прямой вызов `/api/v2/job_templates/{id}/launch/`. Это важное отличие:

- `/launch/` требует авторизацию (Bearer-токен или Basic Auth) — под это `bus-controller` не заточен, авторизационных заголовков он не добавляет.
- `/api/v2/job_templates/{id}/github/` (эндпоинт, который AWX создаёт при включении вебхука) авторизации не требует вовсе — AWX проверяет подлинность запроса по HMAC-подписи тела, посчитанной с помощью `webhook_key`, который AWX генерирует для job template.

Поэтому в `services.json` нужно:
1. В AWX включить **Enable Webhook** на нужном Job Template, выбрать **Webhook Service: GitHub**, сохранить и скопировать сгенерированный **Webhook Key**.
2. В `services[].webhook.url` указать URL вида `.../api/v2/job_templates/{id}/github/` (можно скопировать из поля **Webhook URL** в AWX).
3. В `services[].webhook.hmac_secret` вставить скопированный **Webhook Key**.
4. `services[].webhook.hmac_header` оставить `X-Hub-Signature-256` — именно этот заголовок и формат (`sha256=<hex>`) AWX ожидает для GitHub-style вебхука; `handlers.go` формирует его автоматически из `hmac_secret`.
5. Заголовок `X-GitHub-Event: push` в `headers` желателен — AWX реагирует на его значение при определении типа события; для целей рестарта достаточно `push`.

Тело запроса (`body`) при таком вызове не мёржится напрямую в `extra_vars` job'а — весь JSON из `body` попадёт в задание как переменная `awx_webhook_payload` (доступна плейбуку). Если нужно управлять именно `extra_vars` при запуске, единственный способ без токена — заранее зашить нужные значения в сам Job Template (Prompt on Launch выключен) либо считывать их из `awx_webhook_payload` в плейбуке.

HTTP-проверка считает попытку успешной, если ответ пришёл с кодом 2xx; любой другой код или сетевая ошибка (недоступность, таймаут, TLS-ошибка и т.п.) помечает сервис как `offline` с текстом ошибки в `last_error`. Успешная проверка (TCP или HTTP), которая заняла больше `slow_threshold_ms`, даёт статус `degraded` вместо `online` — в `last_error` при этом попадает сообщение вида `slow response: 1234ms (threshold 1000ms)`. Время последней проверки в миллисекундах всегда доступно в поле `response_time_ms` ответа `GET /api/status`, независимо от того, включена ли degraded-детекция.

## Переменные окружения

| Переменная | Где используется | Назначение |
|---|---|---|
| `SERVICES_CONFIG` | `main.go` | путь к JSON-конфигу сервисов (по умолчанию `services.json`) |
| `PORT` | `main.go`, `docker-compose.yml` | порт HTTP-сервера (по умолчанию `8000`) |

## Запуск

### Локально (без Docker)

```bash
cd backend
go run .
# сервер поднимется на http://localhost:8000, отдавая web/ и API
```

### Через Docker Compose

```bash
docker compose up --build
```

Поднимутся два сервиса:

- `bus-controller` — backend (Go), только API, порт `8000` наружу не пробрасывается;
- `frontend` — nginx со статикой UI и reverse proxy `/api/*` → `bus-controller:8000`, доступен на `http://localhost:8080`.

Порт `8000` backend'а наружу не пробрасывается — снаружи он доступен только через `frontend` (порт `8080`) либо через `oauth2-proxy`, если он настроен. Для использования `oauth2-proxy` нужно настроить соответствующий блок в `docker-compose.yml`:

- `OAUTH2_PROXY_OIDC_ISSUER_URL` — адрес realm в Keycloak;
- `OAUTH2_PROXY_CLIENT_ID` / `OAUTH2_PROXY_CLIENT_SECRET` — данные confidential-клиента в Keycloak;
- `OAUTH2_PROXY_REDIRECT_URL` — callback-URL, зарегистрированный в клиенте;
- `OAUTH2_PROXY_COOKIE_SECRET` — сгенерировать через `openssl rand -base64 32`;
- `OAUTH2_PROXY_UPSTREAMS` — при включении стоит указать `http://frontend:80/`, чтобы авторизация закрывала и UI, и API одним входом (блок пока не менялся автоматически, см. комментарий в `docker-compose.yml`).

Если нужно временно проверить backend API напрямую в обход frontend (для отладки), можно раскомментировать проброс порта `8000:8000` у сервиса `bus-controller` в `docker-compose.yml`.

## HTTP API

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/status` | список статусов всех сервисов (JSON) |
| `POST` | `/api/trigger/{id}` | запустить вебхук для сервиса `{id}` |

UI (`/`) теперь отдаёт отдельный сервис `frontend` (nginx), а не backend — см. `frontend/`.

Пример ответа `GET /api/status`:

```json
[
  {
    "id": "svc1",
    "name": "repomanager",
    "host": "127.0.0.1",
    "port": 8080,
    "check_type": "tcp",
    "status": "online",
    "response_time_ms": 4,
    "last_checked": "2026-07-26T10:00:00Z"
  }
]
```

## Известные проблемы в текущем коде

- `services.json` в репозитории — пример/заготовка на 6 сервисов (`svc1`, `svc-health`, `svc2`–`svc5`); два из них (`svc2`, `svc3`) указывают на один и тот же `host:port` и имеют одинаковое имя `Service 2` — это специально оставлено как демонстрация, для реального использования конфиг нужно заменить своими сервисами.
- Секреты в примере `services.json` (`hmac_secret: "REPLACE_WITH_WEBHOOK_KEY_FROM_AWX_JOB_TEMPLATE_*"`) и в `docker-compose.yml` (`CHANGE_ME`, `CHANGE_ME_32_BYTE_BASE64_SECRET`) — заглушки, обязательно замените их реальными значениями (Webhook Key из AWX, сгенерированные секреты) перед продакшн-разворачиванием.

## Технологии

- Backend: Go 1.22, стандартная библиотека (`net/http`, `net`, `encoding/json`, `crypto/hmac`) — без внешних зависимостей.
- Frontend: чистый HTML/CSS/JS без фреймворков и сборки, раздаётся nginx как отдельный сервис (образ `nginx:1.27-alpine`), который также проксирует `/api/*` на backend.
- Инфраструктура: Docker, docker-compose, oauth2-proxy + Keycloak (OIDC) для авторизации.