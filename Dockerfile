# --- build stage ---
FROM golang:1.22-alpine AS build
WORKDIR /app
COPY backend/go.mod ./
RUN go mod download 2>/dev/null || true
COPY backend/*.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /bus-controller .
 
# --- runtime stage ---
FROM alpine:3.20
WORKDIR /app
COPY certs/root.pem /etc/ssl/certs/ca-certificates.crt
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
COPY --from=build /bus-controller ./bus-controller
 
ENV PORT=8000
EXPOSE 8000
 
ENTRYPOINT ["./bus-controller"]

