FROM golang:1.22-alpine AS builder

WORKDIR /app

# Proxy-based GOPROXY downloads zip archives over HTTPS — no git binary needed.
# goproxy.io and goproxy.cn are reachable from restricted networks (Iran).
ENV GOPROXY=https://goproxy.io,https://goproxy.cn
ENV GONOSUMDB=*

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o trendyol-api-service .

# ─── Final image ──────────────────────────────────────────────────────────────
FROM alpine:3.19

# Try mirrors in order until one works:
#   1. repo.iut.ac.ir  — Isfahan University of Technology (Iran)
#   2. mirror.tuna.tsinghua.edu.cn — Tsinghua University (China)
#   3. mirrors.ustc.edu.cn         — USTC (China)
RUN { \
      printf 'http://repo.iut.ac.ir/repo/alpine/v3.19/main\nhttp://repo.iut.ac.ir/repo/alpine/v3.19/community\n' \
        > /etc/apk/repositories \
      && apk --no-cache add \
           ca-certificates tzdata chromium nss freetype harfbuzz ttf-freefont font-noto; \
    } \
    || { \
      printf 'https://mirror.tuna.tsinghua.edu.cn/alpine/v3.19/main\nhttps://mirror.tuna.tsinghua.edu.cn/alpine/v3.19/community\n' \
        > /etc/apk/repositories \
      && apk --no-cache add \
           ca-certificates tzdata chromium nss freetype harfbuzz ttf-freefont font-noto; \
    } \
    || { \
      printf 'https://mirrors.ustc.edu.cn/alpine/v3.19/main\nhttps://mirrors.ustc.edu.cn/alpine/v3.19/community\n' \
        > /etc/apk/repositories \
      && apk --no-cache add \
           ca-certificates tzdata chromium nss freetype harfbuzz ttf-freefont font-noto; \
    }

ENV TZ=Europe/Istanbul
ENV CHROME_PATH=/usr/bin/chromium-browser

WORKDIR /app
COPY --from=builder /app/trendyol-api-service .

EXPOSE 8080

CMD ["./trendyol-api-service"]
