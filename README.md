# bus-controller

Лёгкий сервис мониторинга доступности сервисов (TCP-порт или HTTP `/health`-эндпоинт) на бэкенде Go с веб-интерфейсом (тёмная тема, живые карточки статусов) и возможностью вручную запустить вебхук (например, AWX/Ansible job) для перезапуска сервиса прямо из UI.

## Возможности

- Периодическая проверка доступности списка сервисов с настраиваемым интервалом и таймаутом. Для каждого сервиса можно выбрать тип проверки: TCP-соединение до `host:port` или HTTP GET-запрос на health-эндпоинт (по умолчанию `/health`).
- Веб-интерфейс с карточками сервисов: статус (`online` / `degraded` / `offline` / `checking…`), время последней проверки и время ответа в мс, текст последней ошибки.
- Кнопка **Restart** на карточке — отправляет POST-запрос на настроенный вебхук AWX (эндпоинт вида `.../github/` или `.../gitlab/`, созданный опцией **Enable Webhook** на Job Template), без API-токена, опционально с HMAC-SHA256 подписью тела запроса (`X-Hub-Signature-256`) или статичным токеном в заголовке (`X-Gitlab-Token`).
- Frontend отдаётся по HTTPS (nginx с собственными сертификатами из `certs/`).

## Структура репозитория

```
.
├── Dockerfile                   # сборка Go-бинарника backend'а + рантайм-образ (только API)
├── docker-compose.yml           # оркестрация: bus-controller (backend) + frontend (nginx, HTTPS)
├── certs/
│   ├── root.pem                  # CA-сертификат, встраивается в backend-образ для проверки TLS исходящих запросов (вебхуки)
│   ├── chain.pem                  # сертификат (+ цепочка), которым nginx терминирует HTTPS для frontend
│   ├── wildcard.key                # приватный ключ к chain.pem
│   └── wildcard.pem                 # запасной/полный сертификат, сейчас нигде не подключён
├── backend/
│   ├── main.go                   # точка входа, роутинг HTTP (/api/*)
│   ├── config.go                  # загрузка и валидация services.json
│   ├── checker.go                  # фоновые TCP/HTTP-проверки, хранилище статусов
│   ├── handlers.go                  # HTTP-хендлеры: /api/status, /api/trigger/{id}
│   ├── go.mod                        # модуль bus-controller, Go 1.22, без внешних зависимостей
│   └── services.json                  # конфиг сервисов и вебхуков (пример/заготовка)
└── frontend/
    ├── nginx.conf                # HTTPS на 443, раздача статики + reverse proxy /api/* → bus-controller:8000
    ├── index.html                 # разметка страницы
    ├── app.js                      # опрос /api/status, рендер карточек, вызов /api/trigger
    └── style.css                    # тёмная тема оформления
```

Backend и frontend — отдельные сервисы docker-compose. Backend отдаёт только JSON API (`/api/status`, `/api/trigger/{id}`) и ничего не знает о UI. Frontend не собирается в отдельный образ: сервис `frontend` в `docker-compose.yml` использует готовый образ `nginx:1.27-alpine`, а `nginx.conf`, статические файлы и `certs/` подключаются volume'ами — правки в них подхватываются рестартом контейнера, без пересборки.

## Как это работает

1. При старте `main.go` читает путь к конфигу из переменной окружения `SERVICES_CONFIG` (по умолчанию `services.json`) и парсит список сервисов через `config.go`.
2. `checker.go` запускает горутину, которая с интервалом `check_interval_seconds` параллельно (`sync.WaitGroup`) проверяет каждый сервис одним из двух способов, в зависимости от его `check_type`:
   - `tcp` (по умолчанию) — пытается открыть TCP-соединение до `host:port` с таймаутом `tcp_timeout_seconds`;
   - `http` — выполняет HTTP GET на health-URL (по умолчанию `http://host:port/health`, настраивается через `health_scheme` / `health_path` / `health_url`) с таймаутом `http_timeout_seconds`; ответ с кодом 2xx считается успешным, всё остальное (сетевые ошибки, коды вне диапазона 2xx) — `offline`.

   Для каждой проверки замеряется время выполнения (`response_time_ms`). Если проверка прошла успешно, но заняла больше `slow_threshold_ms` (глобально или per-service), статус — `degraded`, а не `online`. Итоговый статус (`online` / `degraded` / `offline`) вместе со временем ответа и текстом последней ошибки сохраняется в потокобезопасном `StatusStore`.
3. `handlers.go` отдаёт:
   - `GET /api/status` — JSON-массив текущих статусов всех сервисов;
   - `POST /api/trigger/{id}` — находит сервис по `id`, собирает HTTP-запрос по настройкам его `webhook` (URL, метод, заголовки, тело), при наличии `hmac_secret` добавляет подпись HMAC-SHA256 в заголовок `hmac_header` в формате `sha256=<hex>` (совместимо с проверкой подписи AWX-вебхука для GitHub-сервиса), отправляет запрос и возвращает результат клиенту.

   Backend не раздаёт статику UI — этим занимается отдельный сервис `frontend` (nginx). Для исходящих HTTPS-запросов (например, к AWX) backend-образ доверяет CA из `certs/root.pem` (см. `Dockerfile`, `SSL_CERT_FILE`).
4. Frontend — статический сайт (`index.html`/`app.js`/`style.css`), который отдаёт nginx по HTTPS (см. `frontend/nginx.conf`, сертификаты — `certs/chain.pem` + `certs/wildcard.key`). Nginx проксирует все запросы `/api/*` на `bus-controller:8000`, поэтому для браузера frontend и API выглядят как один origin и `app.js` ходит по относительным путям (`/api/status`, `/api/trigger/{id}`) без CORS. `app.js` раз в 10 секунд опрашивает `/api/status` и перерисовывает карточки; при клике на **Restart** дергает `/api/trigger/{id}` и показывает toast с результатом.

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
| `services[].webhook.body` | нет | тело запроса (строка); подстрока `{{ts}}` в нём подставляется автоматически (`handlers.go`) и заменяется на текущее время в unix-наносекундах перед каждой отправкой — удобно, чтобы тело запроса не повторялось от вызова к вызову |
| `services[].webhook.hmac_secret` | нет | если задан — тело подписывается HMAC-SHA256; итоговая подпись отправляется в заголовке `hmac_header` в формате `sha256=<hex>` |
| `services[].webhook.hmac_header` | обязательно, если задан `hmac_secret` | заголовок для подписи (например `X-Hub-Signature-256` для AWX); дефолта в коде нет — если `hmac_secret` задан, а заголовок не указан, подпись уйдёт в заголовок с пустым именем |

Валидация (`config.go`) требует, чтобы у каждого сервиса были `id`, `host` и `port`; иначе приложение не стартует. Если указан `check_type`, он должен быть `"tcp"` или `"http"` — любое другое значение тоже приводит к ошибке при старте.

> **Конфиг перечитывается только при старте процесса.** `main.go` вызывает `LoadConfig` один раз перед запуском сервера — hot-reload не реализован. После правки `services.json` нужно перезапустить процесс (`docker compose restart bus-controller` или заново `go run .`), иначе изменения не появятся ни в `/api/status`, ни на странице. В `docker-compose.yml` файл `backend/services.json` подключён volume'ом (`:ro`) в контейнер, поэтому пересборка образа для правки конфига не требуется — достаточно рестарта контейнера.

### Запуск Job template в AWX через webhook (без API-токена)

Кнопка Restart рассчитана на встроенный механизм **Enable Webhook** в AWX (Job Template → Enable Webhook → Webhook Service: `GitHub` или `GitLab`), а не на прямой вызов `/api/v2/job_templates/{id}/launch/`. Это важное отличие:

- `/launch/` требует авторизацию (Bearer-токен или Basic Auth) — под это `bus-controller` не заточен, авторизационных заголовков он не добавляет.
- `/api/v2/job_templates/{id}/github/` (или `/gitlab/`) — эндпоинты, которые AWX создаёт при включении вебхука, авторизации не требуют вовсе.

Вариант **GitHub** (HMAC-подпись тела):
1. В AWX включить **Enable Webhook**, выбрать **Webhook Service: GitHub**, сохранить и скопировать **Webhook Key**.
2. `services[].webhook.url` — `.../api/v2/job_templates/{id}/github/`.
3. `services[].webhook.hmac_secret` — скопированный **Webhook Key**.
4. `services[].webhook.hmac_header` — `X-Hub-Signature-256`; `handlers.go` формирует подпись `sha256=<hex>` автоматически.
5. Заголовок `X-GitHub-Event: push` в `headers` желателен.

Вариант **GitLab** (статичный токен в заголовке, без HMAC) — именно так настроен пример в `backend/services.json`:
1. В AWX выбрать **Webhook Service: GitLab**, скопировать **Webhook Key**.
2. `services[].webhook.url` — `.../api/v2/job_templates/{id}/gitlab/`.
3. `webhook.hmac_secret`/`webhook.hmac_header` оставить пустыми.
4. Webhook Key передать обычным заголовком через `webhook.headers`, например `{"X-Gitlab-Token": "..."}`.

Тело запроса (`body`) при таком вызове не мёржится напрямую в `extra_vars` job'а — весь JSON из `body` попадёт в задание как переменная `awx_webhook_payload` (доступна плейбуку). Если нужно управлять именно `extra_vars` при запуске, единственный способ без токена — заранее зашить нужные значения в сам Job Template (Prompt on Launch выключен) либо считывать их из `awx_webhook_payload` в плейбуке.

HTTP-проверка считает попытку успешной, если ответ пришёл с кодом 2xx; любой другой код или сетевая ошибка (недоступность, таймаут, TLS-ошибка и т.п.) помечает сервис как `offline` с текстом ошибки в `last_error`. Успешная проверка (TCP или HTTP), которая заняла больше `slow_threshold_ms`, даёт статус `degraded` вместо `online` — в `last_error` при этом попадает сообщение вида `slow response: 1234ms (threshold 1000ms)`. Время последней проверки в миллисекундах всегда доступно в поле `response_time_ms` ответа `GET /api/status`, независимо от того, включена ли degraded-детекция.

## Переменные окружения

| Переменная | Где используется | Назначение |
|---|---|---|
| `SERVICES_CONFIG` | `main.go` | путь к JSON-конфигу сервисов (по умолчанию `services.json`, в docker-compose — `/app/config/services.json`) |
| `PORT` | `main.go`, `docker-compose.yml` | порт HTTP-сервера backend'а (по умолчанию `8000`) |

## Запуск

### Локально (без Docker)

```bash
cd backend
go run .
# сервер поднимется на http://localhost:8000 и отдаёт только JSON API (/api/status, /api/trigger/{id})
# для UI отдельно нужен nginx/статик-сервер поверх frontend/, либо открыть frontend/index.html
# с ручной настройкой прокси /api/* на localhost:8000
```

### Через Docker Compose

`docker-compose.yml` для сервиса `bus-controller` использует готовый образ (`image: bus-controller:1.1.2`), а не `build:`-контекст — поэтому `docker compose up` сам backend-образ не соберёт. Сначала нужно собрать его из корневого `Dockerfile`:

```bash
docker build -t bus-controller:1.1.2 .
docker compose up -d
```

Поднимутся два сервиса:

- `bus-controller` — backend (Go), только API на порту `8000`; наружу порт не пробрасывается (можно временно раскомментировать `ports: 8000:8000` в `docker-compose.yml` для отладки).
- `frontend` — готовый образ `nginx:1.27-alpine` с примонтированными `frontend/nginx.conf`, статикой (`index.html`/`app.js`/`style.css`) и `certs/`; слушает **HTTPS на 443** (`ssl_certificate` = `certs/chain.pem`, `ssl_certificate_key` = `certs/wildcard.key`, `server_name bus.home.local`) и проксирует `/api/*` → `bus-controller:8000`. Свой образ для frontend не собирается — правки в файлах `frontend/` подхватываются рестартом контейнера (`docker compose restart frontend`), без пересборки.

Доступ: `https://bus.home.local/` (или `https://localhost/`, приняв самоподписанный/несовпадающий по имени сертификат) — используются сертификаты из `certs/`, авторизация (oauth2-proxy/Keycloak и т.п.) в текущем `docker-compose.yml` не настроена.

## HTTP API

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/status` | список статусов всех сервисов (JSON) |
| `POST` | `/api/trigger/{id}` | запустить вебхук для сервиса `{id}` |

UI (`/`) отдаёт отдельный сервис `frontend` (nginx), а не backend — см. `frontend/`.

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

- `services.json` в репозитории — пример/заготовка на 2 сервиса (`svc1` — TCP-проверка, `svc2` — HTTP-проверка) с реальным на вид приватным IP `192.168.0.34`; заголовок `X-Gitlab-Token: paste_webhook_key_here` — заглушка, замените реальным Webhook Key из AWX перед использованием кнопки Restart. Перед коммитом стоит убедиться, что в файл не попадают настоящие адреса/токены из прод-окружения.
- `certs/wildcard.key` — приватный ключ, лежит в репозитории в открытом виде вместе с сертификатами. Стоит проверить, не должен ли он быть в `.gitignore`/секрет-хранилище, и не утёк ли он куда-то ещё.
- `certs/wildcard.pem` ни в `Dockerfile`, ни в `frontend/nginx.conf`, ни где-либо ещё не используется — либо мёртвый файл, либо забыли подключить.
- Авторизация (oauth2-proxy/Keycloak и т.п.) в `docker-compose.yml` не описана и не настроена — снаружи `frontend` на 443 открыт без логина, если контейнер доступен по сети.
- Сервис `bus-controller` в `docker-compose.yml` объявлен через `image: bus-controller:1.1.2` без `build:`-секции — `docker compose up --build` не пересоберёт backend из исходников; образ нужно собирать вручную (`docker build -t bus-controller:1.1.2 .`) перед каждым релизом новой версии кода.

## Технологии

- Backend: Go 1.22, стандартная библиотека (`net/http`, `net`, `encoding/json`, `crypto/hmac`) — без внешних зависимостей.
- Frontend: чистый HTML/CSS/JS без фреймворков и сборки, раздаётся nginx как отдельный сервис (образ `nginx:1.27-alpine`) по HTTPS, который также проксирует `/api/*` на backend.
- Инфраструктура: Docker, docker-compose.
