# NodeX 多阶段构建
# 阶段0: 拉取上游源码（默认 wooxi/nodex 主仓库，可用 SOURCE_REPO 覆盖）
FROM alpine/git AS source
ARG SOURCE_REPO=https://github.com/wooxi/nodex.git
ARG SOURCE_REF=main
RUN git clone --depth 1 --branch ${SOURCE_REF} ${SOURCE_REPO} /src \
    && rm -rf /src/.git

# 阶段1: 前端构建
FROM node:20-alpine AS frontend
# 国内环境可设 NPM_REGISTRY=https://registry.npmmirror.com
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /build/webui
COPY --from=source /src/webui/package.json /src/webui/package-lock.json ./
RUN npm config set registry $NPM_REGISTRY && npm ci --no-audit --no-fund
COPY --from=source /src/webui/ ./
RUN npm run build
# 产物输出到 ../internal/web/dist（vite outDir 配置）

# 阶段2: Go 后端构建
FROM golang:1.26-alpine AS backend
# 国内环境可设 GOPROXY=https://goproxy.cn,direct
ARG GOPROXY=https://proxy.golang.org,direct
ARG VERSION=dev
WORKDIR /build
COPY --from=source /src/go.mod /src/go.sum ./
RUN GOPROXY=$GOPROXY go mod download
COPY --from=source /src/cmd/ ./cmd/
COPY --from=source /src/internal/ ./internal/
# 前端产物供 embed（vite outDir 配置为 ../internal/web/dist）
COPY --from=frontend /build/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X main.version=$VERSION" -o nodex ./cmd/nodex

# 阶段3: 运行镜像
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates openssl curl unzip \
    && rm -rf /var/lib/apt/lists/*

# xray / hysteria 内核（与 OpenWrt 部署同版本）
ARG XRAY_VERSION=v26.3.27
ARG HYSTERIA_VERSION=app/v2.12.0
RUN curl -sL -o /tmp/xray.zip https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/Xray-linux-64.zip \
    && unzip -o /tmp/xray.zip -d /tmp/xray-ext xray && mv /tmp/xray-ext/xray /usr/bin/xray && chmod +x /usr/bin/xray \
    && curl -sL -o /usr/bin/hysteria https://github.com/apernet/hysteria/releases/download/${HYSTERIA_VERSION}/hysteria-linux-amd64 \
    && chmod +x /usr/bin/hysteria && rm -rf /tmp/xray.zip /tmp/xray-ext

COPY --from=backend /build/nodex /usr/bin/nodex
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME ["/etc/nodex"]
EXPOSE 8888
ENTRYPOINT ["/entrypoint.sh"]
