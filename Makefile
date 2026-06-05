BINARY   = trendyol-api-service
IMAGE    = trendyol-api-service
ARCHIVE  = trendyol-api-service.tar.gz

.PHONY: run build tidy vendor docker-build docker-run fix-docker-dns save load

run:
	go run .

build:
	go build -ldflags="-s -w" -o $(BINARY) .

tidy:
	go mod tidy && go mod vendor

vendor:
	go mod vendor

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 --env-file .env $(IMAGE)

# ── Server helpers ────────────────────────────────────────────────────────────

# Fix Docker daemon DNS on the server (run once as root, then restart docker).
# Solves "temporary error" / DNS timeout during docker build on Iranian servers.
fix-docker-dns:
	@echo '{"dns": ["8.8.8.8", "1.1.1.1"]}' | sudo tee /etc/docker/daemon.json
	sudo systemctl restart docker
	@echo "Docker daemon restarted with DNS 8.8.8.8 / 1.1.1.1"

# Build the image locally and save it as a tar.gz archive.
# Use this when the server has no internet access at all.
save:
	docker build -t $(IMAGE) .
	docker save $(IMAGE) | gzip > $(ARCHIVE)
	@echo "Saved to $(ARCHIVE) — transfer to server with scp or rsync"

# Load a previously saved image on the server.
load:
	docker load < $(ARCHIVE)
	@echo "Image loaded — run: docker compose up -d"
