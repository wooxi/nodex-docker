# NodeX · Container

NodeX 的容器镜像。内置 Xray 与 Hysteria 2 内核，常驻一个 Web 管理界面。

[English](README.en.md) · [源码仓库](https://github.com/wooxi/nodex) · [路由器版](https://github.com/wooxi/nodex-luci) · [控制面板](https://github.com/wooxi/xpanel)

---

## 说明

本仓库只做打包，不含 Go 源码。`Dockerfile` 在多阶段构建中从 [wooxi/nodex](https://github.com/wooxi/nodex) 拉取源码，编译前端与二进制，最后与两个内核一起装进运行时镜像。

功能、协议支持与面板对接说明见 [主仓库](https://github.com/wooxi/nodex)。本仓库只描述容器特有的部署方式。

镜像同时发布到 GHCR：

```bash
docker pull ghcr.io/wooxi/nodex-docker:latest
```

## 部署

`docker-compose.yml`：

```yaml
services:
  nodex:
    image: ghcr.io/wooxi/nodex-docker:latest
    container_name: nodex
    restart: unless-stopped
    network_mode: host
    volumes:
      - ./data:/etc/nodex
    environment:
      - TZ=Asia/Shanghai
```

```bash
docker compose up -d
```

访问 `http://<host>:8888`，首次进入设置管理密码。

`network_mode: host` 是必需的：节点端口由面板下发，逐个映射不现实。

### 自行构建

```bash
docker build --build-arg SOURCE_REF=main -t nodex:latest .
```

国内网络可加两个参数：

```bash
docker build \
  --build-arg NPM_REGISTRY=https://registry.npmmirror.com \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -t nodex:latest .
```

构建参数：

| 参数 | 默认值 | 说明 |
|---|---|---|
| `SOURCE_REPO` | `https://github.com/wooxi/nodex.git` | 源码地址 |
| `SOURCE_REF` | `main` | 分支或 tag |
| `NPM_REGISTRY` | `https://registry.npmjs.org` | 前端依赖源 |
| `GOPROXY` | `https://proxy.golang.org,direct` | Go 模块代理 |
| `XRAY_VERSION` | 见 Dockerfile | 内置内核版本 |
| `HYSTERIA_VERSION` | 见 Dockerfile | 内置内核版本 |

## 数据

挂载到 `/etc/nodex`：

| 内容 | 说明 |
|---|---|
| `config.json` | 全部配置，由界面生成 |
| `bin/` | 界面内更新过的内核，优先于镜像自带的版本 |
| `hy2.crt` / `hy2.key` | 首次启动生成的自签证书，节点未单独配置证书时使用 |
| 日志 | 节点与内核运行日志 |

内核可以在界面内更新，更新后的二进制落在 `bin/` 中，容器重建后仍然生效，不会被镜像自带版本覆盖。

## 对接面板

1. 面板 → 服务器 → 添加节点（v2ray 或 hysteria），记下**节点 ID**
2. 面板 → 系统设置 → 获取**通信密钥**
3. `http://<host>:8888` → 面板对接：填面板地址与密钥
4. 节点管理 → 新增节点：填节点 ID，选协议
5. 启动

面板可以是 [XPanel](https://github.com/wooxi/xpanel)、Xboard、V2Board 或任何兼容 UniProxy 的实现。

## 兼容性

| 项 | 要求 |
|---|---|
| 平台 | `linux/amd64` |
| 运行时 | 基于 `debian:bookworm-slim`，已内置 ca-certificates、openssl |
| 网络 | 需 host 网络或等价的全端口可达 |

## 构建与发布

打 tag 触发 GitHub Actions 构建并推送 `ghcr.io/wooxi/nodex-docker`。上游 nodex 有提交时通过 `repository_dispatch` 触发，另有每日定时检查以避免遗漏。

## 许可

本项目采用 MIT 许可证，见 [LICENSE](LICENSE)。
