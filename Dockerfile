# syntax=docker/dockerfile:1
# Сборка: статический бинарник без CGO (SQLite и Git — на чистом Go).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/teampulse ./cmd/teampulse \
 && mkdir -p /out/data

# Запуск: distroless — без shell и пакетного менеджера, от непривилегированного пользователя.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/teampulse /teampulse
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV ADDR=:8080 DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s CMD ["/teampulse", "healthcheck"]
ENTRYPOINT ["/teampulse"]
CMD ["serve"]
