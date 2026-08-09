# NodeX Docker 版

NodeX 节点管家——**Docker 部署版发布仓库**（Vue3 + Element Plus 界面，独立 Web 服务）。

> 开发源码见 [wooxi/nodex](https://github.com/wooxi/nodex)（主仓库）；本仓库发布 Docker 镜像。

## 快速开始

```bash
git clone https://github.com/wooxi/nodex-docker
cd nodex-docker
# 国内网络建议加镜像参数：
docker build --build-arg NPM_REGISTRY=https://registry.npmmirror.com \
             --build-arg GOPROXY=https://goproxy.cn,direct -t nodex:latest .
docker compose up -d
```

- 管理界面：`http://<IP>:8888`（首次访问设置管理密码）
- 配置数据持久化在 `./data/`（配置、日志、证书、**更新后的内核**）
- 镜像内置 xray + hysteria 内核，开箱即用

## 镜像

GHCR：`ghcr.io/wooxi/nodex-docker:latest`（打 tag 自动构建发布）

## 功能

- 多节点管理（每节点独立 xray/hysteria2 进程）
- Xboard 面板对接（UniProxy 协议）
- 协议：vless(+Reality/TLS/WS) / vmess / trojan / shadowsocks / hysteria2
- 转发出站（XrayR 转发模式）
- 内核总开关、核心一键下载/更新（持久化到数据卷）
- 流量统计、在线用户/IP、内核崩溃自动拉起
