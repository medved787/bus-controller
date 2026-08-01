# --- build stage ---
FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
RUN go mod download 2>/dev/null || true
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /bus-controller .
 
# --- runtime stage ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /bus-controller ./bus-controller
# services.json НЕ копируем — он подключается через volume в docker-compose.yml,
# чтобы список сервисов можно было менять без пересборки образа.
# Фронтенд теперь отдельный сервис (см. frontend/) — backend отдаёт только API.
 
ENV PORT=8000
EXPOSE 8000
 
ENTRYPOINT ["./bus-controller"]

