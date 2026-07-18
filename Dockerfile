# syntax=docker/dockerfile:1.7

# ---- 前端构建 ----
FROM --platform=$BUILDPLATFORM node:26-alpine AS frontend
WORKDIR /app/frontend
RUN corepack enable
COPY frontend/package.json frontend/pnpm-lock.yaml* ./
RUN --mount=type=cache,id=pnpm-store,target=/pnpm/store \
    pnpm config set store-dir /pnpm/store && pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build
# 产物输出到 /app/backend/internal/web/dist (vite outDir 配置为 ../backend/...)

# ---- 后端构建 ----
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
COPY --from=frontend /app/backend/internal/web/dist ./internal/web/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /dengdeng ./cmd/server

# ---- 运行时 ----
FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 1000 dengdeng
USER dengdeng
WORKDIR /app
COPY --from=backend /dengdeng /app/dengdeng
VOLUME /app/data
EXPOSE 9100
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -T 5 -O /dev/null http://127.0.0.1:9100/health || exit 1
ENTRYPOINT ["/app/dengdeng"]
