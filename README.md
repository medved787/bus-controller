# bus-controller

Лёгкий сервис мониторинга доступности бэкенд-сервисов (TCP или HTTP-проверка) с тёмным веб-интерфейсом (живые карточки статусов) и возможностью вручную запустить вебхук (например, job template в AWX/Ansible) для перезапуска сервиса прямо из UI.

## Возможности

- Периодическая проверка списка сервисов с настраиваемым интервалом:
  - **TCP** — попытка открыть TCP-соединение до `host:port`;
  - **HTTP** — GET-запрос к health-эндпоинту, статус `2xx` считается признаком доступности.
- Детекция состояния **degraded** — если проверка прошла успешно, но заняла больше заданного порога времени (настраивается глобально и/или per-service).
- Веб-интерфейс с карточками сервисов: статус (`online` / `degraded` / `offline` / `checking…`), тип проверки, время отклика в мс, время последней проверки, текст последней ошибки.
- Кнопка **Restart** на карточке — отправляет POST-запрос на настроенный вебхук сервиса, с опциональной HMAC-SHA256 подписью тела запроса.
- Docker-образ с раздельными build/runtime стадиями; `services.json` подключается через volume, что позволяет менять список сервисов без пересборки образа.
- Опциональный (закомментированный в `docker-compose.yml`) reverse-proxy `oauth2-proxy` с авторизацией через Keycloak (OIDC), чтобы не открывать панель наружу без логина.

## Структура репозитория

```
.
├── docker-compose.yml          # оркестрация: bus-controller (+ опционально oauth2-proxy)
└── backend/
    ├── Dockerfile               # сборка Go-бинарника (build stage) + рантайм-образ (alpine)
    ├── main.go                  # точка входа, роутинг HTTP
    ├── config.go                # загрузка и валидация services.json
    ├── checker.go                # фоновые TCP/HTTP-проверки, хранилище статусов
    ├── handlers.go               # HTTP-хендлеры: /api/status, /api/trigger/{id}
    ├── go.mod                    # модуль bus-controller, Go 1.22
    ├── services.json             # конфиг сервисов и вебхуков (пример)
    └── web/
        ├── index.html            # разметка страницы
        ├── app.js                 # опрос /api/status, рендер карточек, вызов /api/trigger
        └── style.css               # тёмная тема оформления
```

## Как это работает

1. При старте `main.go` читает путь к конфигу из переменной окружения `SERVICES_CONFIG` (по умолчанию `services.json`) и парсит список сервисов через `LoadConfig` в `config.go`.
2. `checker.go` создаёт `StatusStore` (потокобезопасное хранилище статусов, изначально `unknown` для всех сервисов) и запускает фоновую проверку:
   - выполняется сразу при старте, затем — по тикеру с интервалом `check_interval_seconds`;
   - для каждого сервиса проверка запускается в отдельной горутине (`sync.WaitGroup`), то есть все сервисы проверяются параллельно;
   - способ проверки определяется полем `check_type` сервиса: `tcp` (по умолчанию, таймаут `tcp_timeout_seconds`) или `http` (GET-запрос к `HealthCheckURL()`, таймаут `http_timeout_seconds`);
   - если проверка успешна, но заняла дольше порога `slow_threshold_ms` (per-service или глобального), статус выставляется в `degraded`, а не `online`; при неуспехе — `offline` с текстом ошибки.
3. `handlers.go` отдаёт:
   - `GET /api/status` — JSON-массив текущих статусов всех сервисов;
   - `POST /api/trigger/{id}` — находит сервис по `id`, собирает HTTP-запрос по настройкам его `webhook` (URL, метод, заголовки, тело), при наличии `hmac_secret` добавляет подпись HMAC-SHA256 в заголовок (`hmac_header`, по умолчанию `X-Hub-Signature`), отправляет запрос (таймаут клиента 10с) и возвращает клиенту результат (`success`, `status_code`, `message`);
   - `/` — статика из папки `web/` (сам UI, путь настраивается переменной `WEB_DIR`, по умолчанию `./web`).
4. Фронтенд (`app.js`) раз в 10 секунд опрашивает `/api/status` и перерисовывает карточки (цвет полосы слева зависит от статуса: зелёный — `online`, жёлтый — `degraded`, красный — `offline`); при клике на **Restart** дёргает `/api/trigger/{id}` и показывает toast с результатом.

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
        "headers": { "Content-Type": "application/json" },
        "body": "{}",
        "hmac_secret": "",
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
      "webhook": { "...": "..." }
    }
  ]
}
```

Поля верхнего уровня:

| Поле                       | Обязательное           | Описание                                                                 |
| --------------------------- | ----------------------- | -------------------------------------------------------------------------- |
| `check_interval_seconds`    | нет (по умолчанию 10)  | как часто проверять сервисы                                                |
| `tcp_timeout_seconds`       | нет (по умолчанию 3)   | таймаут для проверок типа `tcp`                                            |
| `http_timeout_seconds`      | нет (по умолчанию 3)   | таймаут для проверок типа `http`                                           |
| `slow_threshold_ms`         | нет (по умолчанию 0)   | глобальный порог (мс), после которого успешная проверка становится `degraded`; `0` — детекция отключена |
| `services[]`                | да, минимум 1 элемент  | список отслеживаемых сервисов                                             |

Поля сервиса (`services[]`):

| Поле                        | Обязательное                          | Описание                                                                                     |
| ---------------------------- | -------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `id`                         | да                                    | уникальный идентификатор, используется в URL `/api/trigger/{id}`                               |
| `name`                       | нет                                   | отображаемое имя в UI                                                                          |
| `host` / `port`              | да                                    | адрес, который проверяется, а также используется при построении URL для HTTP-проверки          |
| `check_type`                 | нет (по умолчанию `tcp`)              | `tcp` — открыть TCP-соединение; `http` — GET-запрос к health-эндпоинту                          |
| `health_path`                | нет (по умолчанию `/health`)          | путь для HTTP-проверки (используется вместе с `host:port`, если не задан `health_url`)          |
| `health_url`                 | нет                                   | полный URL для HTTP-проверки; если задан — `host`/`port`/`health_scheme`/`health_path` для построения URL не используются |
| `health_scheme`              | нет (по умолчанию `http`)             | схема для построения URL из `host:port`, когда `health_url` не задан                             |
| `slow_threshold_ms`          | нет (по умолчанию — глобальное значение) | порог для этого сервиса; `0` — использовать глобальный порог; отрицательное значение — отключить degraded-детекцию для сервиса |
| `webhook.url`                | нет                                   | если пусто — `/api/trigger/{id}` вернёт `success: false` с сообщением "webhook url not configured for this service" |
| `webhook.method`             | нет                                   | HTTP-метод запроса к вебхуку                                                                    |
| `webhook.headers`            | нет                                   | произвольные заголовки запроса                                                                  |
| `webhook.body`               | нет                                   | тело запроса (строка)                                                                           |
| `webhook.hmac_secret`        | нет                                   | если задан — тело подписывается HMAC-SHA256                                                     |
| `webhook.hmac_header`        | нет (по умолчанию `X-Hub-Signature`) | заголовок для подписи                                                                            |

Валидация (`config.go`, функция `LoadConfig`) требует:
- у каждого сервиса заданы `id`, `host` и `port` (иначе приложение не стартует);
- `id` уникален среди всех сервисов;
- `check_type`, если задан, равен `tcp` или `http` — иное значение приводит к ошибке загрузки конфига.

> Пример `services.json` в репозитории содержит демонстрационные сервисы `svc1`, `svc-health`, `svc2`, `svc3` (два последних — с одинаковым именем `Service 2` и одинаковым `host:port`); для реального использования конфиг нужно заменить своими сервисами и реальными секретами вебхуков.

## Переменные окружения

| Переменная        | Где используется                | Назначение                                                    |
| ----------------- | -------------------------------- | --------------------------------------------------------------- |
| `SERVICES_CONFIG` | `main.go`                       | путь к JSON-конфигу сервисов (по умолчанию `services.json`)    |
| `WEB_DIR`         | `main.go`                       | путь к статике фронтенда (по умолчанию `./web`)                |
| `PORT`            | `main.go`, `docker-compose.yml` | порт HTTP-сервера (по умолчанию `8000`)                        |

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

`docker-compose.yml` запускает сервис `bus-controller` (образ `bus-controller:1.0`), пробрасывает порт `8000:8000` наружу и монтирует `./backend/services.json` в контейнер как `/app/config/services.json` (read-only), путь к которому передаётся через `SERVICES_CONFIG`.

В файле также есть закомментированный блок `oauth2-proxy` — reverse-proxy с авторизацией через Keycloak (OIDC), который можно включить, чтобы не открывать панель напрямую. Для этого нужно раскомментировать сервис и настроить:

- `OAUTH2_PROXY_OIDC_ISSUER_URL` — адрес realm в Keycloak;
- `OAUTH2_PROXY_CLIENT_ID` / `OAUTH2_PROXY_CLIENT_SECRET` — данные confidential-клиента в Keycloak;
- `OAUTH2_PROXY_REDIRECT_URL` — callback-URL, зарегистрированный в клиенте;
- `OAUTH2_PROXY_COOKIE_SECRET` — сгенерировать через `openssl rand -base64 32`;

а также убрать (или ограничить) прямой проброс порта `8000:8000` у сервиса `bus-controller`, чтобы доступ шёл только через прокси на порту `4180`.

### Сборка Docker-образа вручную

`backend/Dockerfile` собирает статический бинарник в build-стадии (`golang:1.22-alpine`) и копирует его вместе с папкой `web/` в минимальный runtime-образ (`alpine:3.20`). Файл `services.json` в образ **не** копируется — он подключается через volume в `docker-compose.yml`, что позволяет менять список сервисов без пересборки образа.

```bash
cd backend
docker build -t bus-controller:1.0 .
```

## HTTP API

| Метод  | Путь                | Описание                                                        |
| ------ | -------------------- | ------------------------------------------------------------------ |
| `GET`  | `/api/status`        | список статусов всех сервисов (JSON)                              |
| `POST` | `/api/trigger/{id}`  | запустить вебхук для сервиса `{id}`                                |
| `GET`  | `/`                   | статические файлы UI из `backend/web` (или `WEB_DIR`)              |

Пример ответа `GET /api/status`:

```json
[
  {
    "id": "svc-tcp1",
    "name": "service1",
    "host": "127.0.0.1",
    "port": 8080,
    "check_type": "tcp",
    "status": "online",
    "response_time_ms": 4,
    "last_checked": "2026-07-26T10:00:00Z"
  },
  {
    "id": "svc-http1",
    "name": "service2",
    "host": "10.0.0.20",
    "port": 8443,
    "check_type": "http",
    "status": "degraded",
    "response_time_ms": 420,
    "last_checked": "2026-07-26T10:00:00Z",
    "last_error": "slow response: 420ms (threshold 300ms)"
  }
]
```

Пример ответа `POST /api/trigger/{id}`:

```json
{
  "success": true,
  "status_code": 202,
  "message": "Accepted"
}
```

## Технологии

- **Backend**: Go 1.22, стандартная библиотека (`net/http`, `net`, `encoding/json`, `crypto/hmac`) — без внешних зависимостей.
- **Frontend**: чистый HTML/CSS/JS без фреймворков и сборки, тёмная тема.
- **Инфраструктура**: Docker (multi-stage build, `golang:1.22-alpine` → `alpine:3.20`), docker-compose, опционально oauth2-proxy + Keycloak (OIDC) для авторизации.