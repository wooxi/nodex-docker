#!/bin/sh
# NodeX 容器入口
set -e

mkdir -p /etc/nodex /etc/nodex/bin

# 首次运行生成默认 hysteria2 证书（节点未单独配置时使用）
if [ ! -f /etc/nodex/hy2.crt ] || [ ! -f /etc/nodex/hy2.key ]; then
  echo "[nodex] 生成默认 hysteria2 证书..."
  openssl req -x509 -newkey rsa:2048 -keyout /etc/nodex/hy2.key -out /etc/nodex/hy2.crt \
    -days 3650 -nodes -subj "/CN=nodex" 2>/dev/null
fi

echo "[nodex] NodeX 容器启动 $(/usr/bin/nodex -version)"
exec /usr/bin/nodex
