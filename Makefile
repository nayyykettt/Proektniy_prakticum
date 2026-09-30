.PHONY: run build test test-integration cover lint docker docker-up

run:            ## Запуск локально (dev-режим, разрешены локальные репозитории)
	ALLOW_LOCAL_REPOS=true go run ./cmd/teampulse serve

build:          ## Сборка бинарника в bin/
	CGO_ENABLED=0 go build -trimpath -o bin/teampulse ./cmd/teampulse

test:           ## Модульные и HTTP-тесты
	go vet ./...
	go test -race ./...

test-integration: ## Сценарий с реальным публичным репозиторием (нужен интернет)
	go test -tags integration -run TestRealRepository -v ./internal/app/

cover:          ## Покрытие тестами
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...

docker:         ## Сборка Docker-образа
	docker build -t teampulse:latest .

docker-up:      ## Запуск через docker compose (нужен файл .env)
	docker compose up -d --build
