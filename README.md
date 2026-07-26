# bus-controller (Port Monitor)

Лёгкий сервис мониторинга доступности TCP-портов на бэкенде Go с веб-интерфейсом (тёмная тема, живые карточки статусов) и возможностью вручную запустить вебхук (например, AWX/Ansible job) для перезапуска сервиса прямо из UI.

> Внутреннее имя Go-модуля и бинарника — `port-monitor`, контейнер в docker-compose называется `port-monitor`.

## Возможности

- Периодическая TCP-проверка списка сервисов (`host:port`) с настраиваемым интервалом и таймаутом.
- Веб-интерфейс с карточками сервисов: статус (`online` / `offline` / `checking…`), время последней проверки, текст последней ошибки.
- Кнопка **Restart** на карточке — отправляет POST-запрос на настроенный вебхук (например, запуск job template в AWX), с опциональной HMAC-SHA256 подписью тела запроса.
- Опциональный reverse-proxy с авторизацией через Keycloak (`oauth2-proxy`), чтобы не открывать панель наружу без логина.

## Структура репозитория

```
.
├── docker-compose.yml          # оркестрация: port-monitor + oauth2-proxy
└── backend/
    ├── Dockerfile               # сборка Go-бинарника + рантайм-образ
    ├── main.go                  # точка входа, роутинг HTTP
    ├── config.go                # загрузка и валидация services.json
    ├── checker.go                # фоновые TCP-проверки, хранилище статусов
    ├── handlers.go               # HTTP-хендлеры: /api/status, /api/trigger/{id}
    ├── go.mod                    # модуль port-monitor, Go 1.22
    ├── services.json             # конфиг сервисов и вебхуков (пример)
    └── web/
        ├── index.html            # разметка страницы
        ├── app.js                 # опрос /api/status, рендер карточек, вызов /api/trigger
        └── style.css               # тёмная тема оформления
```

## Как это работает

1. При старте `main.go` читает путь к конфигу из переменной окружения `SERVICES_CONFIG` (по умолчанию `services.json`) и парсит список сервисов через `config.go`.
2. `checker.go` запускает горутину, которая с интервалом `check_interval_seconds` параллельно (`sync.WaitGroup`) пытается открыть TCP-соединение до каждого `host:port` с таймаутом `tcp_timeout_seconds`. Результат (online/offline + текст ошибки) сохраняется в потокобезопасном `StatusStore`.
3. `handlers.go` отдаёт:
   - `GET /api/status` — JSON-массив текущих статусов всех сервисов;
   - `POST /api/trigger/{id}` — находит сервис по `id`, собирает HTTP-запрос по настройкам его `webhook` (URL, метод, заголовки, тело), при наличии `hmac_secret` добавляет подпись HMAC-SHA256 в заголовок (`hmac_header`, по умолчанию `X-Hub-Signature`), отправляет запрос и возвращает результат клиенту;
   - `/` — статика из папки `web/` (сам UI).
4. Фронтенд (`app.js`) раз в 10 секунд опрашивает `/api/status` и перерисовывает карточки; при клике на **Restart** дергает `/api/trigger/{id}` и показывает toast с результатом.

## Конфигурация (`services.json`)

```json
{
  "check_interval_seconds": 10,
  "tcp_timeout_seconds": 3,
  "services": [
    {
      "id": "svc1",
      "name": "repomanager",
      "host": "127.0.0.1",
      "port": 8080,
      "webhook": {
        "url": "https://awx.example.com/api/v2/job_templates/1/launch/",
        "method": "POST",
        "headers": { "Content-Type": "application/json" },
        "body": "{}",
        "hmac_secret": "",
        "hmac_header": "X-Hub-Signature"
      }
    }
  ]
}
```

Поля:

| Поле | Обязательное | Описание |
|---|---|---|
| `check_interval_seconds` | нет (по умолчанию 10) | как часто проверять порты |
| `tcp_timeout_seconds` | нет (по умолчанию 3) | таймаут TCP-подключения |
| `services[].id` | да | уникальный идентификатор, используется в URL `/api/trigger/{id}` |
| `services[].name` | нет | отображаемое имя в UI |
| `services[].host` / `port` | да | адрес, который проверяется |
| `services[].webhook.url` | нет | если пусто — кнопка Restart вернёт ошибку "webhook url not configured" |
| `services[].webhook.method` | нет (по умолчанию `POST`) | HTTP-метод запроса к вебхуку |
| `services[].webhook.headers` | нет | произвольные заголовки запроса |
| `services[].webhook.body` | нет | тело запроса (строка) |
| `services[].webhook.hmac_secret` | нет | если задан — тело подписывается HMAC-SHA256 |
| `services[].webhook.hmac_header` | нет (по умолчанию `X-Hub-Signature`) | заголовок для подписи |

Валидация (`config.go`) требует, чтобы у каждого сервиса были `id`, `host` и `port`; иначе приложение не стартует.

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

По умолчанию порт `8000` наружу не пробрасывается — доступ предполагается только через `oauth2-proxy` на порту `4180`. Для этого нужно настроить блок `oauth2-proxy` в `docker-compose.yml`:

- `OAUTH2_PROXY_OIDC_ISSUER_URL` — адрес realm в Keycloak;
- `OAUTH2_PROXY_CLIENT_ID` / `OAUTH2_PROXY_CLIENT_SECRET` — данные confidential-клиента в Keycloak;
- `OAUTH2_PROXY_REDIRECT_URL` — callback-URL, зарегистрированный в клиенте;
- `OAUTH2_PROXY_COOKIE_SECRET` — сгенерировать через `openssl rand -base64 32`.

Если нужно временно открыть UI без авторизации (для локальной проверки), можно раскомментировать проброс порта `8000:8000` у сервиса `port-monitor` в `docker-compose.yml`.

## HTTP API

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/status` | список статусов всех сервисов (JSON) |
| `POST` | `/api/trigger/{id}` | запустить вебхук для сервиса `{id}` |
| `GET` | `/` | статические файлы UI из `backend/web` |

Пример ответа `GET /api/status`:

```json
[
  {
    "id": "svc1",
    "name": "repomanager",
    "host": "127.0.0.1",
    "port": 8080,
    "status": "online",
    "last_checked": "2026-07-26T10:00:00Z"
  }
]
```

## Известные проблемы в текущем коде

- **`backend/Dockerfile` не собирается как есть**: копируется только `main.go`, тогда как пакет `main` состоит ещё из `checker.go`, `config.go` и `handlers.go` — их тоже нужно скопировать перед `go build`.
- **`backend/Dockerfile` копирует несуществующую папку**: строка `COPY frontend ./frontend` ссылается на каталог `frontend`, которого нет — фронтенд лежит в `backend/web`. Также не копируется `services.json`, без которого приложение не запустится в контейнере.
- `services.json` в репозитории — лишь пример/заготовка (содержит дублирующиеся `id`/`name` для `svc2`/`svc3`), для реального использования его нужно заменить своими сервисами.
- Секреты в `docker-compose.yml` (`CHANGE_ME`, `CHANGE_ME_32_BYTE_BASE64_SECRET`) — заглушки, обязательно замените перед продакшн-разворачиванием.

## Технологии

- Backend: Go 1.22, стандартная библиотека (`net/http`, `net`, `encoding/json`, `crypto/hmac`) — без внешних зависимостей.
- Frontend: чистый HTML/CSS/JS без фреймворков и сборки.
- Инфраструктура: Docker, docker-compose, oauth2-proxy + Keycloak (OIDC) для авторизации.
