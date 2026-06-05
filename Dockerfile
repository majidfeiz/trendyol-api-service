FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY . .

# Build from vendored dependencies — no network access required.
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o trendyol-api-service .

# ─── Final image ──────────────────────────────────────────────────────────────
FROM alpine:3.19

# Use ArvanCloud mirror (Iranian CDN) instead of the default Alpine CDN.
RUN sed -i 's|https://dl-cdn.alpinelinux.org/alpine|https://mirror.arvancloud.ir/alpine|g' \
        /etc/apk/repositories \
    && apk --no-cache add \
        ca-certificates \
        tzdata \
        chromium \
        nss \
        freetype \
        harfbuzz \
        ttf-freefont \
        font-noto

ENV TZ=Europe/Istanbul
ENV CHROME_PATH=/usr/bin/chromium-browser

WORKDIR /app
COPY --from=builder /app/trendyol-api-service .

EXPOSE 8080

CMD ["./trendyol-api-service"]
