FROM golang:1.22-alpine AS builder

WORKDIR /app

# Bypass proxy.golang.org (blocked on some servers); fetch directly from source repos.
# GONOSUMDB skips checksum.sum.golang.org which may also be unreachable.
ENV GOPROXY=direct
ENV GONOSUMDB=*

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o trendyol-api-service .

# ─── Final image ──────────────────────────────────────────────────────────────
FROM alpine:3.19

# Chromium + dependencies for headless mode
RUN apk --no-cache add \
    ca-certificates \
    tzdata \
    chromium \
    nss \
    freetype \
    harfbuzz \
    ttf-freefont \
    font-noto

ENV TZ=Europe/Istanbul
# Path headless Chrome binary (used by chromedp when CHROME_PATH is set)
ENV CHROME_PATH=/usr/bin/chromium-browser

WORKDIR /app
COPY --from=builder /app/trendyol-api-service .

EXPOSE 8080

CMD ["./trendyol-api-service"]
