package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
	_ "time/tzdata" // 内置时区数据库（OpenWrt 无 zoneinfo 也能解析 Asia/Shanghai）

	"github.com/wooxi/nodex/internal/config"
	"github.com/wooxi/nodex/internal/manager"
	"github.com/wooxi/nodex/internal/web"
)

var version = "0.3.2"

func main() {
	var (
		cfgPath = flag.String("config", "", "配置文件路径（默认 /etc/nodex/config.json）")
		showVer = flag.Bool("version", false, "显示版本")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("nodex %s\n", version)
		return
	}

	if *cfgPath == "" {
		*cfgPath = config.DefaultConfigPath
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("[nodex] 加载配置失败: %v", err)
	}

	// 确保数据目录存在
	if err := os.MkdirAll(cfg.DataDir(), 0o755); err != nil {
		log.Fatalf("[nodex] 创建数据目录失败: %v", err)
	}

	mgr := manager.New(cfg, *cfgPath)
	mgr.Rebuild(cfg)

	// init 脚本调用 start/stop/restart 时管理全部节点
	switch {
	case len(os.Args) > 1 && os.Args[1] == "start":
		mgr.StartAll()
		// 保持前台运行（procd 管理）
	case len(os.Args) > 1 && os.Args[1] == "stop":
		mgr.StopAll()
		return
	case len(os.Args) > 1 && os.Args[1] == "restart":
		mgr.StopAll()
		time.Sleep(300 * time.Millisecond)
		mgr.StartAll()
		return
	}

	_ = context.Background()

	// 服务模式：自动启动所有启用节点（容器重启 / 开机自启后节点自动恢复）
	mgr.StartAll()

	srv := web.New(cfg, *cfgPath, mgr)
	listen := cfg.Web.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", listen, cfg.Web.Port)
	log.Printf("[nodex] NodeX v%s Web 管理界面: http://%s", version, addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("[nodex] Web 服务启动失败: %v", err)
	}
}
