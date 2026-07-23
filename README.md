# Port Monitor

Веб-страница для мониторинга доступности TCP-портов двух сервисов + кнопки
запуска webhook'ов AWX. Написано на Go, разворачивается в докере.

## Структура

```
port-monitor/
├── backend/
│   ├── main.go        # весь бэкенд: TCP-чекер, API, статика
│   ├── go.mod
│   └── Dockerfile
├── frontend/
│   └── index.html      # страница с автообновлением каждые 10 сек
├── docker-compose.yml   # приложение + oauth2-proxy (Keycloak)
└── README.md
```

## 1. Настройка сервисов

Откройте `backend/main.go`, функция `loadServices()`, и замените
плейсхолдеры на реальные данные:

```go
{
    ID:            "svc1",
    Name:          "Service 1",
    Host:          "10.0.0.1",                  // хост сервиса
    Port:          8080,                          // порт для TCP-проверки
    WebhookURL:    "https://awx.example.com/api/v2/job_templates/1/callback/?key=ВАШ_КЛЮЧ",
    WebhookMethod: http.MethodPost,
},
```

`WebhookURL` — это ваш AWX callback URL с `host_config_key` в query-параметре
`key`. Если у вашего вебхука другая схема (например, ключ передаётся в теле
запроса или в заголовке), скорректируйте `triggerHandler` в `main.go`.

## 2. Локальный запуск без докера (для проверки)

```bash
cd backend
go run .
```

Откройте http://localhost:8000 — авторизации на этом этапе нет.

## 3. Запуск в докере (с авторизацией через Keycloak)

### 3.1. Настройка клиента в Keycloak

1. В нужном realm создайте клиента, например `port-monitor`.
2. Client authentication: **On** (confidential client).
3. Standard flow: **включён**.
4. Valid redirect URIs: `https://monitor.example.com/oauth2/callback`
   (или ваш реальный домен).
5. Сохраните **Client secret** со вкладки Credentials.

### 3.2. Настройка docker-compose.yml

В сервисе `oauth2-proxy` замените:

- `OAUTH2_PROXY_OIDC_ISSUER_URL` — на адрес вашего realm:
  `https://<keycloak-host>/realms/<realm-name>`
- `OAUTH2_PROXY_CLIENT_ID` / `OAUTH2_PROXY_CLIENT_SECRET` — данные клиента из Keycloak
- `OAUTH2_PROXY_REDIRECT_URL` — реальный домен, на котором будет открываться страница
- `OAUTH2_PROXY_COOKIE_SECRET` — сгенерируйте командой:
  ```bash
  openssl rand -base64 32
  ```

### 3.3. Запуск

```bash
docker compose up -d --build
```

Страница будет доступна на `http://<host>:4180` (или через ваш reverse-proxy /
домен, если он настроен перед oauth2-proxy). При заходе пользователь
перенаправляется на Keycloak для логина, после успешной авторизации —
на страницу мониторинга.

Сам `port-monitor` порт наружу не пробрасывает — единственная точка входа
это `oauth2-proxy`, поэтому без валидной сессии Keycloak до приложения
не достучаться.

## 4. Как это работает

- Бэкенд каждые 10 секунд пытается открыть TCP-соединение до каждого
  сервиса (`net.DialTimeout`), результат (online/offline) хранится в памяти.
- Фронтенд каждые 10 секунд опрашивает `GET /api/status` и перерисовывает
  карточки сервисов.
- Кнопка «Запустить» шлёт `POST /api/trigger?id=<service_id>` на бэкенд,
  который проксирует запрос на реальный AWX webhook URL и возвращает
  результат (код ответа + тело) — так реальный webhook URL никогда не
  светится в браузере пользователя.

## 5. Что стоит доработать дальше

- Вынести конфигурацию сервисов из кода в config.json / переменные окружения.
- Добавить HTTPS перед oauth2-proxy (обычно через nginx/traefik/caddy).
- Ограничить доступ по email-домену или группе в Keycloak
  (OAUTH2_PROXY_EMAIL_DOMAINS, OAUTH2_PROXY_ALLOWED_GROUPS).
- При желании — история/лог вызовов webhook'ов (сейчас показывается только
  в toast-уведомлении на странице).
