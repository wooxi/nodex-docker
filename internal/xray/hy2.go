package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wooxi/nodex/internal/config"
)

// Hy2Manager 管理单个节点的 hysteria2 进程（官方 hysteria 二进制）
// 认证采用 auth.http 模式回调 NodeX，实现 per-user 流量统计：
//   客户端 auth = 用户 uuid → NodeX 认证 API 返回 {ok, id: uid} → /traffic 按 uid 统计
type Hy2Manager struct {
	mu         sync.RWMutex
	cfg        *config.Node // 节点配置
	global     *config.Config
	dir        string // 节点数据目录
	apiPort    int    // traffic API 端口
	authURL    string // 认证回调地址
	secret     string // traffic API 密钥
	remotePort int    // 面板返回的端口（面板优先）
	cmd        *exec.Cmd
	userMu     sync.RWMutex
	userIdx    map[string]int64 // uuid -> uid（认证回调 O(1) 查询）
}

// SetRemotePort 设置面板端口（hysteria 节点时 hy2 端口跟随面板）
func (m *Hy2Manager) SetRemotePort(p int) { m.remotePort = p }

func (m *Hy2Manager) globalCfg() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.global
}

func NewHy2Manager(node *config.Node, global *config.Config, dir string, apiPort int) *Hy2Manager {
	return &Hy2Manager{cfg: node, global: global, dir: dir, apiPort: apiPort}
}

func (m *Hy2Manager) UpdateConfig(node *config.Node, global *config.Config) {
	m.mu.Lock()
	m.cfg = node
	m.global = global
	m.mu.Unlock()
}

// SetAuth 设置认证回调与 traffic API 密钥
func (m *Hy2Manager) SetAuth(authURL, secret string) {
	m.authURL = authURL
	m.secret = secret
}

// SetUsers 注入用户列表（构建 uuid→uid 索引，供认证回调 O(1) 查询）
func (m *Hy2Manager) SetUsers(users []User) {
	m.userMu.Lock()
	defer m.userMu.Unlock()
	idx := map[string]int64{}
	for _, u := range users {
		idx[u.UUID] = u.ID
	}
	m.userIdx = idx
}

// AuthUser 认证回调：auth=uuid → 返回 uid
func (m *Hy2Manager) AuthUser(auth string) (int64, bool) {
	m.userMu.RLock()
	defer m.userMu.RUnlock()
	if m.userIdx == nil {
		return 0, false
	}
	uid, ok := m.userIdx[auth]
	return uid, ok
}

func (m *Hy2Manager) configPath() string { return filepath.Join(m.dir, "hy2", "config.yaml") }
func (m *Hy2Manager) logPath() string    { return filepath.Join(m.dir, "hy2", "hy2.log") }
func (m *Hy2Manager) pidFile() string    { return filepath.Join(m.dir, "hy2", "hy2.pid") }

func (m *Hy2Manager) IsRunning() bool {
	if m.cmd != nil && m.cmd.Process != nil {
		// 注意：不能只用 ProcessState.Exited()——被信号杀死的进程 WIFEXITED 为 false，
		// 会误报存活导致看门狗不拉起。用 Signal(0) 做存活探测。
		return m.cmd.Process.Signal(syscall.Signal(0)) == nil
	}
	if data, err := os.ReadFile(m.pidFile()); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if syscall.Kill(pid, 0) == nil {
				// 端口佐证：确认 traffic API 端口有监听（容器重启后 pid 可能被复用）
				return portListening(m.apiPort)
			}
		}
	}
	return false
}

func (m *Hy2Manager) Pid() int {
	if m.cmd != nil && m.cmd.Process != nil {
		return m.cmd.Process.Pid
	}
	if data, err := os.ReadFile(m.pidFile()); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return pid
	}
	return 0
}

// BuildConfig 生成 hysteria2 服务器配置（YAML）
func (m *Hy2Manager) BuildConfig(authURL, trafficSecret string) (string, error) {
	cfg := m.cfg.Node.Hy2
	port := cfg.Port
	if m.remotePort > 0 {
		port = m.remotePort // 面板端口优先
	}
	cert, key := cfg.CertPath, cfg.KeyPath
	if cert == "" {
		cert = m.globalCfg().System.CertPath
		key = m.global.System.KeyPath
	}
	var b strings.Builder
	fmt.Fprintf(&b, "listen: 0.0.0.0:%d\n", port)
	fmt.Fprintf(&b, "tls:\n  cert: %s\n  key: %s\n", cert, key)
	if cfg.IgnoreBW {
		b.WriteString("ignoreClientBandwidth: true\n")
	} else {
		fmt.Fprintf(&b, "bandwidth:\n  up: %d mbps\n  down: %d mbps\n", cfg.UpMbps, cfg.DownMbps)
	}
	if cfg.Obfs != "" && cfg.Obfs != "none" {
		fmt.Fprintf(&b, "obfs:\n  type: %s\n  salamander:\n    password: %s\n", cfg.Obfs, cfg.ObfsPassword)
	}
	// per-user 认证：HTTP 回调 NodeX
	b.WriteString("auth:\n  type: http\n  http:\n    url: " + authURL + "\n")
	// 流量统计 API
	b.WriteString("trafficStats:\n")
	fmt.Fprintf(&b, "  listen: 127.0.0.1:%d\n", m.apiPort)
	fmt.Fprintf(&b, "  secret: %s\n", trafficSecret)
	return b.String(), nil
}

// Start 生成配置并启动 hysteria2
func (m *Hy2Manager) Start() error {
	// 自己启动的进程还在跑则跳过（Signal(0) 存活探测，信号杀死的进程不视为存活）
	if m.cmd != nil && m.cmd.Process != nil && m.cmd.Process.Signal(syscall.Signal(0)) == nil {
		return nil
	}
	// 残留进程先清理（确保带当前环境重启）
	if data, err := os.ReadFile(m.pidFile()); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if syscall.Kill(pid, 0) == nil {
				m.Stop()
			}
		}
	}
	conf, err := m.BuildConfig(m.authURL, m.secret)
	if err != nil {
		return err
	}
	confPath := m.configPath()
	if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		return err
	}
	bin := m.globalCfg().System.HysteriaPath
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("hysteria 可执行文件不存在: %s", bin)
	}
	logF, _ := os.OpenFile(m.logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	cmd := exec.Command(bin, "server", "-c", confPath)
	cmd.Stdout = logF
	cmd.Stderr = logF
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 hysteria2 失败: %w", err)
	}
	m.cmd = cmd
	os.WriteFile(m.pidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	go cmd.Wait()
	return nil
}

// Stop 停止 hysteria2（同步等待退出）
func (m *Hy2Manager) Stop() error {
	pid := m.Pid()
	if pid > 0 {
		syscall.Kill(pid, syscall.SIGTERM)
		for i := 0; i < 50; i++ {
			if syscall.Kill(pid, 0) != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		syscall.Kill(pid, syscall.SIGKILL)
		time.Sleep(200 * time.Millisecond)
	}
	m.cmd = nil
	os.Remove(m.pidFile())
	return nil
}

func (m *Hy2Manager) Version() string {
	bin := m.globalCfg().System.HysteriaPath
	if _, err := os.Stat(bin); err != nil {
		return "未安装"
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "未知"
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Version") {
			return strings.TrimSpace(line)
		}
	}
	return "未知"
}

// Hy2Traffic /traffic API 的单次结果
type Hy2Traffic struct {
	Tx int64 `json:"tx"`
	Rx int64 `json:"rx"`
}

// FetchTraffic 从 hysteria /traffic API 拉取 per-user 流量（uid -> Traffic）
func (m *Hy2Manager) FetchTraffic(ctx context.Context) (map[int64]Traffic, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/traffic", m.apiPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", m.secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var raw map[string]Hy2Traffic
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析 /traffic 失败: %w", err)
	}
	out := map[int64]Traffic{}
	for key, t := range raw {
		uid, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			continue
		}
		out[uid] = Traffic{Up: t.Tx, Down: t.Rx}
	}
	return out, nil
}

// SnapshotAndDiff 计算 hysteria2 流量增量
func (m *Hy2Manager) SnapshotAndDiff(ctx context.Context, last *map[int64]Traffic) (map[int64]Traffic, error) {
	cur, err := m.FetchTraffic(ctx)
	if err != nil {
		return nil, err
	}
	if *last == nil {
		*last = map[int64]Traffic{}
	}
	diffs := map[int64]Traffic{}
	for uid, v := range cur {
		prev := (*last)[uid]
		d := Traffic{}
		if v.Up > prev.Up {
			d.Up = v.Up - prev.Up
		}
		if v.Down > prev.Down {
			d.Down = v.Down - prev.Down
		}
		if d.Up > 0 || d.Down > 0 {
			diffs[uid] = d
		}
		(*last)[uid] = v
	}
	for uid := range *last {
		if _, ok := cur[uid]; !ok {
			delete(*last, uid)
		}
	}
	return diffs, nil
}
