BINARY=trendyol-api-service

.PHONY: run build tidy docker-build docker-run

run:
	go run .

build:
	go build -ldflags="-s -w" -o $(BINARY) .

tidy:
	go mod tidy

docker-build:
	docker build -t $(BINARY) .

docker-run:
	docker run --rm -p 8080:8080 --env-file .env $(BINARY)
