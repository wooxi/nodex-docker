package xray

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/wooxi/nodex/internal/config"
)

// Manager 管理单个节点的 xray 进程生命周期与配置生成
// 每个节点独立进程、独立数据目录、独立 API 端口
type Manager struct {
	mu      sync.RWMutex
	cfg     *config.Node // 节点配置
	global  *config.Config
	dir     string // 节点数据目录
	apiPort int    // gRPC stats 端口
	cmd     *exec.Cmd
	users   []User // 面板用户（由同步器注入）
	remote  *RemoteConfig // 面板返回的节点配置（端口/网络/TLS）
}

func (m *Manager) globalCfg() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.global
}

// RemoteConfig 面板返回的节点参数（由同步器注入）
type RemoteConfig struct {
	Protocol   string
	Port       int
	Network    string
	TLS        int
	Flow       string
	Cipher     string
	WSHost     string
	WSPath     string
}

func NewManager(node *config.Node, global *config.Config, dir string, apiPort int) *Manager {
	return &Manager{cfg: node, global: global, dir: dir, apiPort: apiPort}
}

// SetUsers 注入面板用户列表
func (m *Manager) SetUsers(users []User) { m.users = users }

// SetRemoteConfig 注入面板返回的节点配置
func (m *Manager) SetRemoteConfig(r *RemoteConfig) { m.remote = r }

// UpdateConfig 以新配置重建（进程不重启，由外部调用 Restart）
func (m *Manager) UpdateConfig(node *config.Node, global *config.Config) {
	m.mu.Lock()
	m.cfg = node
	m.global = global
	m.mu.Unlock()
}

// IsRunning 检查 xray 进程是否存活（进程句柄 + pid 文件 + 端口三重检测）
func (m *Manager) IsRunning() bool {
	if m.cmd != nil && m.cmd.Process != nil {
		// 注意：不能用 ProcessState.Exited()——被信号杀死的进程 WIFEXITED 为 false，
		// 误报存活导致看门狗不拉起。用 Signal(0) 做存活探测。
		return m.cmd.Process.Signal(syscall.Signal(0)) == nil
	}
	pid := m.readPID()
	if pid > 0 {
		if err := syscall.Kill(pid, 0); err == nil {
			// 端口检测：确认是 xray 在监听（容器重启后 pid 可能被复用）
			return portListening(m.apiPort)
		}
	}
	return false
}

func (m *Manager) configPath() string { return filepath.Join(m.dir, "xray", "config.json") }
func (m *Manager) logPath() string        { return filepath.Join(m.dir, "xray", "access.log") }
func (m *Manager) errLogPath() string     { return filepath.Join(m.dir, "xray", "error.log") }
func (m *Manager) pidFile() string        { return filepath.Join(m.dir, "xray", "xray.pid") }

func (m *Manager) readPID() int {
	data, err := os.ReadFile(m.pidFile())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// Pid 返回当前 xray 进程 PID（未运行时返回 0）
func (m *Manager) Pid() int {
	if m.cmd != nil && m.cmd.Process != nil {
		return m.cmd.Process.Pid
	}
	return m.readPID()
}

// portListening 检查本地端口是否有监听（进程存活佐证）
func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (m *Manager) writePID(pid int) {
	os.WriteFile(m.pidFile(), []byte(strconv.Itoa(pid)), 0o644)
}

// Start 生成配置并启动 xray
func (m *Manager) Start() error {
	// 自己启动的进程还在跑则跳过（Signal(0) 存活探测，信号杀死的进程不视为存活）
	if m.cmd != nil && m.cmd.Process != nil && m.cmd.Process.Signal(syscall.Signal(0)) == nil {
		return nil
	}
	// 残留进程（pid 文件记录但非本实例启动，如重启/崩溃遗留）：先清理
	// 确保以当前环境（TZ 等）重启，避免旧进程占用端口
	if m.readPID() > 0 {
		m.Stop()
	}
	conf, err := m.BuildConfig()
	if err != nil {
		return fmt.Errorf("生成 xray 配置失败: %w", err)
	}
	confPath := m.configPath()
	if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(confPath, conf, 0o644); err != nil {
		return err
	}

	bin := m.globalCfg().System.XrayPath
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("xray 可执行文件不存在: %s", bin)
	}

	logDir := filepath.Dir(m.logPath())
	os.MkdirAll(logDir, 0o755)
	accessF, _ := os.OpenFile(m.logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	errF, _ := os.OpenFile(m.errLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)

	cmd := exec.Command(bin, "run", "-c", confPath)
	cmd.Stdout = accessF
	cmd.Stderr = errF
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 xray 失败: %w", err)
	}
	m.cmd = cmd
	m.writePID(cmd.Process.Pid)
	go cmd.Wait()
	return nil
}

// Stop 停止 xray（同步等待退出，确保端口释放）
func (m *Manager) Stop() error {
	pid := m.Pid()
	if pid > 0 {
		syscall.Kill(pid, syscall.SIGTERM)
		// 等待优雅退出（最多 5 秒）
		for i := 0; i < 50; i++ {
			if syscall.Kill(pid, 0) != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		// 强制终止
		syscall.Kill(pid, syscall.SIGKILL)
		time.Sleep(200 * time.Millisecond)
	}
	m.cmd = nil
	os.Remove(m.pidFile())
	return nil
}

// Restart 重启 xray
func (m *Manager) Restart() error {
	m.Stop()
	time.Sleep(300 * time.Millisecond)
	return m.Start()
}

// Version 获取 xray 版本
func (m *Manager) Version() string {
	bin := m.globalCfg().System.XrayPath
	if _, err := os.Stat(bin); err != nil {
		return "未安装"
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "未知"
	}
	first := strings.SplitN(string(out), "\n", 2)[0]
	return strings.TrimSpace(first)
}

// GenRealityKeys 生成 reality 密钥对（X25519）
func GenRealityKeys() (priv, pub, shortID string, err error) {
	privBytes := make([]byte, 32)
	if _, err = rand.Read(privBytes); err != nil {
		return
	}
	privBytes[0] &= 248
	privBytes[31] &= 127
	privBytes[31] |= 64
	pubBytes, err := curve25519.X25519(privBytes, curve25519.Basepoint)
	if err != nil {
		return
	}
	// xray 使用 RawURLEncoding（无 padding 的 URL-safe base64）
	priv = base64.RawURLEncoding.EncodeToString(privBytes)
	pub = base64.RawURLEncoding.EncodeToString(pubBytes)
	shortID = fmt.Sprintf("%x", privBytes[:4])
	return
}

// ---------------- xray 配置生成 ----------------

// User 面板用户
type User struct {
	ID          int64  `json:"id"`
	UUID        string `json:"uuid"`
	SpeedLimit  int64  `json:"speed_limit"`
	DeviceLimit int    `json:"device_limit"`
}

type inbound struct {
	Tag      string          `json:"tag"`
	Listen   string          `json:"listen"`
	Port     int             `json:"port"`
	Protocol string          `json:"protocol"`
	Settings json.RawMessage `json:"settings"`
	Stream   json.RawMessage `json:"streamSettings,omitempty"`
}

// BuildConfig 根据当前配置 + 面板用户生成 xray 配置 JSON
func (m *Manager) BuildConfig() ([]byte, error) {
	users := m.users // 由 Panel 同步器注入

	logLevel := m.globalCfg().System.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}

	out := map[string]any{
		"log": map[string]any{
			"loglevel": logLevel,
			"access":   m.logPath(),
			"error":    m.errLogPath(),
		},
		"api": map[string]any{
			"tag":      "api",
			"listen":   fmt.Sprintf("127.0.0.1:%d", m.apiPort), // 新版 commander 独立监听
			"services": []string{"StatsService"},
		},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{
				"0": map[string]any{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
				},
			},
			"system": map[string]any{
				"statsInboundUplink":   true,
				"statsInboundDownlink": true,
			},
		},
		"inbounds":  []any{},
		"outbounds": m.buildOutbounds(),
		"routing":   m.buildRouting(),
	}

	inbounds := []any{}
	if m.globalCfg().Panel.Enabled && len(users) > 0 {
		inbounds = append(inbounds, m.buildPanelInbound(users)...)
	} else {
		inbounds = append(inbounds, m.buildLocalInbound()...)
	}
	out["inbounds"] = inbounds

	// 健康检查探针（xray 自带 api 用于测试）
	probe := map[string]any{
		"tag":      "probe",
		"listen":   "127.0.0.1",
		"port":     0,
		"protocol": "dokodemo-door",
		"settings": map[string]any{"address": "127.0.0.1"},
	}
	_ = probe

	return json.MarshalIndent(out, "", "  ")
}

// buildOutbounds 出站：转发模式生成 vless+ws+tls 出站（含负载均衡），否则直连
func (m *Manager) buildOutbounds() []any {
	fwd := m.cfg.Forward
	if !fwd.Enabled || len(fwd.Targets) == 0 {
		return []any{map[string]any{"protocol": "freedom", "tag": "direct"}}
	}
	fingerprint := fwd.Fingerprint
	if fingerprint == "" {
		fingerprint = "chrome"
	}
	port := fwd.Targets[0].Port
	if port == 0 {
		port = 443
	}
	outbounds := []any{}
	for i, t := range fwd.Targets {
		p := t.Port
		if p == 0 {
			p = port
		}
		outbounds = append(outbounds, map[string]any{
			"tag":      fmt.Sprintf("forward-%d", i),
			"protocol": "vless",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address":    t.Address,
					"port":       p,
					"users":      []any{map[string]any{
						"id":         fwd.UUID,
						"security":   "auto",
						"encryption": "none",
					}},
				}},
			},
			"streamSettings": map[string]any{
				"network": "ws",
				"security": "tls",
				"tlsSettings": map[string]any{
					"allowInsecure": false,
					"serverName":    fwd.ServerName,
					"fingerprint":   fingerprint,
				},
				"wsSettings": map[string]any{
					"path": fwd.WSPath,
					"headers": map[string]any{
						"Host": fwd.WSHost,
					},
				},
			},
		})
	}
	// 保留直连（API 用）
	outbounds = append(outbounds, map[string]any{"protocol": "freedom", "tag": "direct"})
	return outbounds
}

// buildRouting 路由：转发模式用 balancer 负载均衡，否则默认路由
func (m *Manager) buildRouting() map[string]any {
	fwd := m.cfg.Forward
	rules := []any{
		map[string]any{
			"inboundTag": []string{"api"},
			"outboundTag": "direct",
			"type":       "field",
		},
	}
	if fwd.Enabled && len(fwd.Targets) > 0 {
		selector := []string{}
		for i := range fwd.Targets {
			selector = append(selector, fmt.Sprintf("forward-%d", i))
		}
		return map[string]any{
			"balancers": []any{map[string]any{
				"tag":      "balancer",
				"selector": selector,
				"strategy": map[string]any{"type": "random"},
			}},
			"rules": append(rules, map[string]any{
				"inboundTag": []string{"in-main"},
				"balancerTag": "balancer",
				"type":       "field",
			}),
		}
	}
	return map[string]any{"rules": rules}
}

// buildLocalInbound 本地模式：按表单配置生成单个入站（hysteria2 由独立进程管理，不进 xray）
func (m *Manager) buildLocalInbound() []any {
	cfg := m.cfg.Node
	var inbounds []any

	if cfg.Protocol != "hysteria2" {
		inbounds = append(inbounds, m.xrayInbound(cfg, []User{{ID: 1, UUID: cfg.UUID}}))
	}
	return inbounds
}

// buildPanelInbound 面板模式：按面板返回的节点配置生成入站（hysteria2 由独立进程管理）
func (m *Manager) buildPanelInbound(users []User) []any {
	cfg := m.cfg.Node
	var inbounds []any

	// 面板模式：端口以面板返回为准（自动同步），网络/TLS 以面板返回为准
	port := cfg.Port
	tlsMode := cfg.TLS
	network := "tcp"
	if m.remote != nil {
		if m.remote.Port > 0 {
			port = m.remote.Port
		}
		if m.remote.Network != "" {
			network = m.remote.Network
		}
		// 面板 tls 字段优先（0=关闭 1=TLS 2=Reality）；面板关闭 TLS 时不用 Reality（客户端订阅与节点保持一致）
		if m.remote.TLS == 0 && tlsMode == 2 {
			tlsMode = 0
		}
	}

	node := config.NodeConfig{
		Protocol:   m.cfg.NodeType,
		Port:       port,
		UUID:       "",
		TLS:        tlsMode,
		CertPath:   cfg.CertPath,
		KeyPath:    cfg.KeyPath,
		ServerName: cfg.ServerName,
		Reality:    cfg.Reality,
		SSMethod:   cfg.SSMethod,
	}
	if node.Protocol != "hysteria2" && node.Protocol != "hysteria" {
		inbounds = append(inbounds, m.xrayInboundWithNetwork(node, users, network, m.remote))
	}
	return inbounds
}

// xrayInboundWithNetwork 构建带网络传输的入站（支持 ws/grpc/httpupgrade 等）
func (m *Manager) xrayInboundWithNetwork(cfg config.NodeConfig, users []User, network string, remote *RemoteConfig) any {
	stream := map[string]any{"network": network}

	// 传输层配置（ws path/host 等）
	switch network {
	case "ws", "websocket":
		ws := map[string]any{}
		if remote != nil {
			if remote.WSPath != "" {
				ws["path"] = remote.WSPath
			}
			if remote.WSHost != "" {
				ws["host"] = remote.WSHost
			}
		}
		stream["wsSettings"] = ws
	case "grpc":
		g := map[string]any{}
		if remote != nil && remote.WSPath != "" {
			g["serviceName"] = remote.WSPath
		}
		stream["grpcSettings"] = g
	case "httpupgrade":
		h := map[string]any{}
		if remote != nil && remote.WSPath != "" {
			h["path"] = remote.WSPath
		}
		if remote != nil && remote.WSHost != "" {
			h["host"] = remote.WSHost
		}
		stream["httpupgradeSettings"] = h
	}

	// TLS 层
	if cfg.TLS == 2 {
		serverNames := config.SplitCSV(cfg.Reality.ServerNames)
		shortIDs := config.SplitCSV(cfg.Reality.ShortIDs)
		if len(shortIDs) == 0 {
			shortIDs = []string{""}
		}
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"show":        false,
			"dest":        cfg.Reality.Dest,
			"serverNames": serverNames,
			"privateKey":  cfg.Reality.PrivateKey,
			"shortIds":    shortIDs,
		}
	} else if cfg.TLS == 1 {
		stream["security"] = "tls"
		tlsS := map[string]any{
			"certificates": []any{map[string]any{
				"certificateFile": cfg.CertPath,
				"keyFile":         cfg.KeyPath,
			}},
		}
		if cfg.ServerName != "" {
			tlsS["serverName"] = cfg.ServerName
		}
		stream["tlsSettings"] = tlsS
	} else {
		stream["security"] = "none"
	}

	var settings map[string]any
	switch cfg.Protocol {
	case "vmess":
		clients := []any{}
		for _, u := range users {
			clients = append(clients, map[string]any{
				"id": u.UUID, "email": emailOf(u.ID),
			})
		}
		settings = map[string]any{"clients": clients}
	case "trojan":
		clients := []any{}
		for _, u := range users {
			clients = append(clients, map[string]any{
				"password": u.UUID, "email": emailOf(u.ID),
			})
		}
		settings = map[string]any{"clients": clients}
	case "shadowsocks":
		usersArr := []any{}
		for _, u := range users {
			usersArr = append(usersArr, map[string]any{
				"email": emailOf(u.ID), "password": u.UUID, "method": cfg.SSMethod,
			})
		}
		settings = map[string]any{"users": usersArr}
	default: // vless
		clients := []any{}
		flow := ""
		if cfg.TLS == 2 {
			flow = "xtls-rprx-vision"
		}
		for _, u := range users {
			clients = append(clients, map[string]any{
				"id": u.UUID, "email": emailOf(u.ID), "flow": flow,
			})
		}
		settings = map[string]any{"clients": clients, "decryption": "none"}
	}

	return map[string]any{
		"tag":            "in-main",
		"listen":         "0.0.0.0",
		"port":           cfg.Port,
		"protocol":       cfg.Protocol,
		"settings":       settings,
		"streamSettings": stream,
	}
}

// xrayInbound 构建 vless/vmess/trojan/shadowsocks 入站
func (m *Manager) xrayInbound(cfg config.NodeConfig, users []User) any {
	stream := map[string]any{"network": "tcp"}

	if cfg.TLS == 2 {
		serverNames := config.SplitCSV(cfg.Reality.ServerNames)
		shortIDs := config.SplitCSV(cfg.Reality.ShortIDs)
		if len(shortIDs) == 0 {
			shortIDs = []string{""}
		}
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"show":        false,
			"dest":        cfg.Reality.Dest,
			"serverNames": serverNames,
			"privateKey":  cfg.Reality.PrivateKey,
			"shortIds":    shortIDs,
		}
	} else if cfg.TLS == 1 {
		stream["security"] = "tls"
		stream["tlsSettings"] = map[string]any{
			"certificates": []any{map[string]any{
				"certificateFile": cfg.CertPath,
				"keyFile":         cfg.KeyPath,
			}},
		}
	}

	var settings map[string]any
	switch cfg.Protocol {
	case "vmess":
		clients := []any{}
		for _, u := range users {
			clients = append(clients, map[string]any{
				"id": u.UUID, "email": emailOf(u.ID),
			})
		}
		settings = map[string]any{"clients": clients}
	case "trojan":
		clients := []any{}
		for _, u := range users {
			clients = append(clients, map[string]any{
				"password": u.UUID, "email": emailOf(u.ID),
			})
		}
		settings = map[string]any{"clients": clients}
	case "shadowsocks":
		usersArr := []any{}
		for _, u := range users {
			usersArr = append(usersArr, map[string]any{
				"email": emailOf(u.ID), "password": u.UUID, "method": cfg.SSMethod,
			})
		}
		settings = map[string]any{"users": usersArr}
	default: // vless
		clients := []any{}
		flow := ""
		if cfg.TLS == 2 {
			flow = "xtls-rprx-vision"
		}
		for _, u := range users {
			clients = append(clients, map[string]any{
				"id": u.UUID, "email": emailOf(u.ID), "flow": flow,
			})
		}
		settings = map[string]any{"clients": clients, "decryption": "none"}
	}

	return map[string]any{
		"tag":            "in-main",
		"listen":         "0.0.0.0",
		"port":           cfg.Port,
		"protocol":       cfg.Protocol,
		"settings":       settings,
		"streamSettings": stream,
	}
}

// hy2Inbound 构建 hysteria2 入站（xray 1.8+ 原生支持）
func (m *Manager) hy2Inbound(cfg config.Hy2, users []User) any {
	clients := []any{}
	for _, u := range users {
		clients = append(clients, map[string]any{
			"password": u.UUID, "email": emailOf(u.ID),
		})
	}
	settings := map[string]any{"clients": clients}
	if cfg.Obfs != "none" && cfg.Obfs != "" {
		settings["obfs"] = map[string]any{
			"type":     cfg.Obfs,
			"password": cfg.ObfsPassword,
		}
	}
	if !cfg.IgnoreBW {
		settings["upMbps"] = cfg.UpMbps
		settings["downMbps"] = cfg.DownMbps
	}
	return map[string]any{
		"tag":      "in-hy2",
		"listen":   "0.0.0.0",
		"port":     cfg.Port,
		"protocol": "hysteria2",
		"settings": settings,
	}
}

// emailOf 用户 email 格式：uid@nodex，用于流量统计映射
func emailOf(uid int64) string { return strconv.FormatInt(uid, 10) + "@nodex" }

// ParseEmail 从 xray stat name（user>>>{email}>>>traffic>>>uplink）或 email 解析 uid
func ParseEmail(name string) (int64, bool) {
	// stat name 形如 user>>>1@nodex>>>traffic>>>uplink，取第二段
	if parts := strings.Split(name, ">>>"); len(parts) >= 2 {
		name = parts[1]
	}
	idx := strings.Index(name, "@")
	if idx <= 0 {
		return 0, false
	}
	uid, err := strconv.ParseInt(name[:idx], 10, 64)
	if err != nil {
		return 0, false
	}
	return uid, true
}
