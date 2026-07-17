# ---- 前端构建 ----
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
RUN corepack enable
COPY frontend/package.json frontend/pnpm-lock.yaml* ./
RUN pnpm install
COPY frontend/ ./
RUN pnpm build
# 产物输出到 /app/backend/internal/web/dist (vite outDir 配置为 ../backend/...)

# ---- 后端构建 ----
FROM golang:1.25-alpine AS backend
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /app/backend/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /dengdeng ./cmd/server

# ---- 运行时 ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 1000 dengdeng
USER dengdeng
WORKDIR /app
COPY --from=backend /dengdeng /app/dengdeng
VOLUME /app/data
EXPOSE 9100
ENTRYPOINT ["/app/dengdeng"]
