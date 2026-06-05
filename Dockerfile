FROM golang:1.22-alpine AS builder

WORKDIR /app

# Switch Alpine repos to ArvanCloud mirror (accessible from Iran)
RUN sed -i 's|https://dl-cdn.alpinelinux.org/alpine|https://mirror.arvancloud.ir/alpine|g' \
        /etc/apk/repositories \
    && apk add --no-cache git

# Use goproxy.io as Go module proxy (accessible from Iran).
# Falls back through goproxy.cn then direct if needed.
ENV GOPROXY=https://goproxy.io,https://goproxy.cn,direct
ENV GONOSUMDB=*

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o trendyol-api-service .

# ─── Final image ──────────────────────────────────────────────────────────────
FROM alpine:3.19

# Switch Alpine repos to ArvanCloud mirror (accessible from Iran)
RUN sed -i 's|https://dl-cdn.alpinelinux.org/alpine|https://mirror.arvancloud.ir/alpine|g' \
        /etc/apk/repositories

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
ENV CHROME_PATH=/usr/bin/chromium-browser

WORKDIR /app
COPY --from=builder /app/trendyol-api-service .

EXPOSE 8080

CMD ["./trendyol-api-service"]
