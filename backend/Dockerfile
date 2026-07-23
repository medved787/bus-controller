# --- build stage ---
FROM golang:1.22-alpine AS build
WORKDIR /app
COPY backend/go.mod ./
RUN go mod download 2>/dev/null || true
COPY backend/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /port-monitor .

# --- runtime stage ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /port-monitor ./port-monitor
COPY frontend ./frontend

ENV PORT=8000
EXPOSE 8000

ENTRYPOINT ["./port-monitor"]
