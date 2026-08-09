package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wooxi/nodex/internal/config"
	"github.com/wooxi/nodex/internal/panel"
	"github.com/wooxi/nodex/internal/xray"
)

var versionRe = regexp.MustCompile(`(v?\d+\.\d+\.\d+)`)

// Runtime 单个节点的运行时（xray + hysteria2 + 同步器 + stats）
type Runtime struct {
	Cfg    *config.Node
	Index  int
	Dir    string
	Xray   *xray.Manager
	Hy2    *xray.Hy2Manager
	Stats  *xray.StatsCollector
	Syncer *panel.Syncer

	mu         sync.RWMutex // 保护以下状态字段（看门狗并发读 vs handler 写）
	xrayPaused bool         // 内核总开关：暂停 xray（看门狗不自动拉起）
	hy2Paused  bool         // 内核总开关：暂停 hysteria2
	stopped    bool         // 用户手动停止该节点（看门狗不自动拉起）

	// 版本缓存（避免每次状态轮询 exec 子进程）
	versionMu   sync.Mutex
	xrayVer     string
	hy2Ver      string
	verCacheAt  time.Time
}

// 状态字段安全访问（看门狗读 / handler 写）
func (rt *Runtime) setStopped(v bool)    { rt.mu.Lock(); rt.stopped = v; rt.mu.Unlock() }
func (rt *Runtime) setXrayPaused(v bool) { rt.mu.Lock(); rt.xrayPaused = v; rt.mu.Unlock() }
func (rt *Runtime) setHy2Paused(v bool)  { rt.mu.Lock(); rt.hy2Paused = v; rt.mu.Unlock() }
func (rt *Runtime) setCfg(c *config.Node) { rt.mu.Lock(); rt.Cfg = c; rt.mu.Unlock() }

// stateSnapshot 原子读取看门狗关心的状态
func (rt *Runtime) stateSnapshot() (enabled, stopped, xrayPaused, hy2Paused bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.Cfg.Enabled, rt.stopped, rt.xrayPaused, rt.hy2Paused
}

// cfgSnapshot 原子读取节点配置指针
func (rt *Runtime) cfgSnapshot() *config.Node {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.Cfg
}

// cachedVersion 带 TTL 的版本缓存（60s），避免状态轮询反复 exec
func (rt *Runtime) cachedVersion(kind string) string {
	rt.versionMu.Lock()
	defer rt.versionMu.Unlock()
	if time.Since(rt.verCacheAt) < 60*time.Second {
		if kind == "xray" {
			return rt.xrayVer
		}
		return rt.hy2Ver
	}
	rt.xrayVer = rt.Xray.Version()
	rt.hy2Ver = rt.Hy2.Version()
	rt.verCacheAt = time.Now()
	if kind == "xray" {
		return rt.xrayVer
	}
	return rt.hy2Ver
}

// Manager 多节点管理器
type Manager struct {
	global    *config.Config
	cfgPath   string
	mu        sync.Mutex
	nodes     map[string]*Runtime
	order     []string
	watchdog  chan struct{}
	watchdogM sync.Mutex
}

func New(global *config.Config, cfgPath string) *Manager {
	return &Manager{
		global:  global,
		cfgPath: cfgPath,
		nodes:   map[string]*Runtime{},
	}
}

// setGlobal 原子替换全局配置（immutable swap：读取方持旧指针读旧内容，安全）
func (m *Manager) setGlobal(cfg *config.Config) {
	m.mu.Lock()
	m.global = cfg
	m.mu.Unlock()
}

// globalCfg 原子获取全局配置指针
func (m *Manager) globalCfg() *config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.global
}

// ---------- 看门狗：内核崩溃自动拉起 ----------

func (m *Manager) startWatchdog() {
	m.watchdogM.Lock()
	defer m.watchdogM.Unlock()
	if m.watchdog != nil {
		return
	}
	ch := make(chan struct{})
	m.watchdog = ch
	go m.watchdogLoop(ch)
}

func (m *Manager) stopWatchdog() {
	m.watchdogM.Lock()
	ch := m.watchdog
	m.watchdog = nil
	m.watchdogM.Unlock()
	if ch != nil {
		close(ch)
	}
}

// watchdogLoop 每 10s 检查一次：启用节点任一内核未运行则自动拉起（受内核总开关暂停状态约束）
func (m *Manager) watchdogLoop(stop chan struct{}) {
	// 退出时自我清理：若 m.watchdog 仍指向本 goroutine 的 channel 则置 nil，
	// 避免 start/stop 交错后 startWatchdog 误判"已在运行"导致看门狗永久失效
	defer func() {
		m.watchdogM.Lock()
		if m.watchdog == stop {
			m.watchdog = nil
		}
		m.watchdogM.Unlock()
	}()
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			for _, rt := range m.Runtimes() {
				enabled, stopped, xrayPaused, hy2Paused := rt.stateSnapshot()
				if !enabled || stopped {
					continue
				}
				need := false
				if !xrayPaused && !rt.Xray.IsRunning() {
					log.Printf("[nodex][%s] 检测到 xray 未运行，自动拉起", rt.Cfg.ID)
					need = true
				}
				if !hy2Paused && !rt.Hy2.IsRunning() {
					log.Printf("[nodex][%s] 检测到 hysteria2 未运行，自动拉起", rt.Cfg.ID)
					need = true
				}
				if need {
					m.startOne(rt)
				}
			}
		}
	}
}

// Rebuild 按配置重建全部节点运行时（保留已运行状态则调用方先 Stop）
func (m *Manager) Rebuild(cfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.global = cfg
	m.nodes = map[string]*Runtime{}
	m.order = nil
	for i, n := range cfg.Nodes {
		dir := cfg.NodeDataDir(n)
		apiPort := cfg.System.APIPortBase + i
		hy2Port := cfg.System.Hy2APIPortBase + i
		xm := xray.NewManager(n, cfg, dir, apiPort)
		hy2 := xray.NewHy2Manager(n, cfg, dir, hy2Port)
		stats := xray.NewStatsCollector()
		syncer := panel.NewSyncer(n, cfg, dir, xm, hy2, stats)
		rt := &Runtime{
			Cfg: n, Index: i, Dir: dir,
			Xray: xm, Hy2: hy2, Stats: stats, Syncer: syncer,
		}
		m.nodes[n.ID] = rt
		m.order = append(m.order, n.ID)
	}
}

// Runtimes 按配置顺序返回节点运行时
func (m *Manager) Runtimes() []*Runtime {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Runtime, 0, len(m.order))
	for _, id := range m.order {
		if rt, ok := m.nodes[id]; ok {
			out = append(out, rt)
		}
	}
	return out
}

func (m *Manager) Get(id string) *Runtime {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodes[id]
}

// StartAll 启动所有启用节点
func (m *Manager) StartAll() {
	m.resetKernelPause()
	m.startWatchdog()
	for _, rt := range m.Runtimes() {
		if !rt.Cfg.Enabled {
			continue
		}
		m.startOne(rt)
	}
}

// Start 启动单个节点（UI 操作，不受内核总开关暂停影响）
func (m *Manager) Start(id string) {
	rt := m.Get(id)
	if rt == nil || !rt.cfgSnapshot().Enabled {
		return
	}
	m.startOne(rt)
}

// startOne 启动单个节点的全部组件
func (m *Manager) startOne(rt *Runtime) {
	rt.setStopped(false) // 显式启动：清除用户停止意图（看门狗恢复监控）
	cfg := m.globalCfg()
	rtCfg := rt.cfgSnapshot()
	// 注入认证信息
	rt.Hy2.SetAuth(fmt.Sprintf("http://127.0.0.1:%d/api/hy2-auth?node=%s", cfg.Web.Port, rtCfg.ID), rt.Syncer.Hy2Secret())
	// 本地模式（面板未启用）注入本地用户
	if !cfg.Panel.Enabled && rtCfg.Node.UUID != "" {
		rt.Xray.SetUsers([]xray.User{{ID: 1, UUID: rt.Cfg.Node.UUID}})
		rt.Hy2.SetUsers([]xray.User{{ID: 1, UUID: rt.Cfg.Node.UUID}})
	}
	rt.Syncer.Start()
	if err := rt.Xray.Start(); err != nil {
		log.Printf("[nodex][%s] 启动 xray 失败: %v", rt.Cfg.ID, err)
	} else {
		go m.connectStats(rt)
	}
	if err := rt.Hy2.Start(); err != nil {
		log.Printf("[nodex][%s] 启动 hysteria2 失败: %v", rt.Cfg.ID, err)
	}
}

// StopAll 停止所有节点
func (m *Manager) StopAll() {
	m.stopWatchdog()
	m.resetKernelPause()
	for _, rt := range m.Runtimes() {
		m.Stop(rt.Cfg.ID)
	}
}

// Stop 停止单个节点（标记用户停止意图，看门狗不会自动拉起）
func (m *Manager) Stop(id string) {
	rt := m.Get(id)
	if rt == nil {
		return
	}
	rt.setStopped(true)
	rt.Syncer.Stop()
	rt.Xray.Stop()
	rt.Hy2.Stop()
	rt.Stats.Close()
}

// Restart 重启单个节点（先清流量快照，避免重启后旧基线吞掉新流量）
func (m *Manager) Restart(id string) {
	m.Stop(id)
	time.Sleep(300 * time.Millisecond)
	if rt := m.Get(id); rt != nil && rt.cfgSnapshot().Enabled {
		rt.Stats.Reset()
		rt.Syncer.ResetHy2()
		m.startOne(rt)
	}
}

// StopAllXray 停止所有节点的 xray
func (m *Manager) StopAllXray() {
	for _, rt := range m.Runtimes() {
		rt.setXrayPaused(true)
		rt.Xray.Stop()
	}
}

// StartAllXray 启动所有启用节点的 xray
func (m *Manager) StartAllXray() {
	for _, rt := range m.Runtimes() {
		rt.setXrayPaused(false)
		if !rt.cfgSnapshot().Enabled {
			continue
		}
		if err := rt.Xray.Start(); err != nil {
			log.Printf("[nodex][%s] 启动 xray 失败: %v", rt.Cfg.ID, err)
		} else {
			rt.Stats.Reset()
			go m.connectStats(rt)
		}
	}
}

// StopAllHy2 停止所有节点的 hysteria2
func (m *Manager) StopAllHy2() {
	for _, rt := range m.Runtimes() {
		rt.setHy2Paused(true)
		rt.Hy2.Stop()
		rt.Syncer.ResetHy2()
	}
}

// StartAllHy2 启动所有启用节点的 hysteria2
func (m *Manager) StartAllHy2() {
	cfg := m.globalCfg()
	for _, rt := range m.Runtimes() {
		rt.setHy2Paused(false)
		if !rt.cfgSnapshot().Enabled {
			continue
		}
		rt.Hy2.SetAuth(fmt.Sprintf("http://127.0.0.1:%d/api/hy2-auth?node=%s", cfg.Web.Port, rt.cfgSnapshot().ID), rt.Syncer.Hy2Secret())
		if err := rt.Hy2.Start(); err != nil {
			log.Printf("[nodex][%s] 启动 hysteria2 失败: %v", rt.Cfg.ID, err)
		} else {
			rt.Syncer.ResetHy2()
		}
	}
}

// resetKernelPause 清除内核总开关暂停状态（全量重启/重建后）
func (m *Manager) resetKernelPause() {
	for _, rt := range m.Runtimes() {
		rt.setXrayPaused(false)
		rt.setHy2Paused(false)
	}
}

// ApplyConfig 应用新配置，最小化重启范围：
//   - 节点增删/顺序变化或 system 变更 → 全量重建重启
//   - 仅节点参数变化 → 只重启该节点内核
//   - 仅面板参数/节点对接 ID 变化 → 原地更新同步器，不重启内核
func (m *Manager) ApplyConfig(old, new *config.Config) {
	oldNodes := append([]*config.Node(nil), old.Nodes...)

	sameOrder := len(oldNodes) == len(new.Nodes)
	if sameOrder {
		for i := range new.Nodes {
			if oldNodes[i].ID != new.Nodes[i].ID {
				sameOrder = false
				break
			}
		}
	}

	// 节点结构/顺序或系统配置变更：全量重建（API 端口按索引派生，必须一致）
	if !sameOrder || !reflect.DeepEqual(old.System, new.System) {
		m.StopAll()
		m.setGlobal(new)
		m.Rebuild(new)
		m.StartAll()
		return
	}

	m.setGlobal(new)

	// 面板全局参数变化：所有节点同步器原地更新（不重启内核），并触发立即同步
	panelChanged := !reflect.DeepEqual(old.Panel, new.Panel)
	if panelChanged {
		for _, newN := range new.Nodes {
			if rt := m.Get(newN.ID); rt != nil {
				rt.setCfg(newN)
				rt.Syncer.UpdateConfig(newN, new)
			}
		}
	}

	restartIDs := []string{}
	for i, newN := range new.Nodes {
		oldN := oldNodes[i]
		if reflect.DeepEqual(oldN, newN) {
			continue
		}
		rt := m.Get(newN.ID)
		if rt == nil {
			continue
		}
		if !panelChanged {
			rt.setCfg(newN)
			rt.Syncer.UpdateConfig(newN, new)
		}
		// 内核相关参数（协议/端口/转发/启用状态）变化 → 重启该节点
		if !reflect.DeepEqual(oldN.Node, newN.Node) ||
			!reflect.DeepEqual(oldN.Forward, newN.Forward) ||
			oldN.Enabled != newN.Enabled {
			restartIDs = append(restartIDs, newN.ID)
		}
	}
	for _, id := range restartIDs {
		m.Restart(id)
	}
	if panelChanged {
		m.SyncAll()
	}
}

// SyncAll 触发全节点同步（面板配置变更后）
func (m *Manager) SyncAll() {
	for _, rt := range m.Runtimes() {
		rt.Syncer.Start()
	}
}

// connectStats 连接节点 xray API（无限重试，指数退避 2s→60s 封顶）
// 避免 xray 启动慢/偶发失败时流量统计永久丢失
func (m *Manager) connectStats(rt *Runtime) {
	apiPort := m.globalCfg().System.APIPortBase + rt.Index
	backoff := 2 * time.Second
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := rt.Stats.Connect(ctx, fmt.Sprintf("127.0.0.1:%d", apiPort))
		cancel()
		if err == nil {
			log.Printf("[nodex][%s] 已连接 xray API", rt.cfgSnapshot().ID)
			return
		}
		time.Sleep(backoff)
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

// Status 返回全部节点状态
func (m *Manager) Status() []map[string]any {
	out := []map[string]any{}
	for _, rt := range m.Runtimes() {
		out = append(out, rt.Status())
	}
	return out
}

// Status 单节点状态（版本走 60s 缓存，避免轮询频繁 exec）
func (rt *Runtime) Status() map[string]any {
	cfg := rt.cfgSnapshot()
	return map[string]any{
		"id":       cfg.ID,
		"name":     cfg.Name,
		"enabled":  cfg.Enabled,
		"protocol": cfg.Node.Protocol,
		"xray": map[string]any{
			"running": rt.Xray.IsRunning(),
			"version": rt.cachedVersion("xray"),
			"pid":     rt.Xray.Pid(),
		},
		"hy2": map[string]any{
			"running": rt.Hy2.IsRunning(),
			"version": rt.cachedVersion("hysteria"),
			"pid":     rt.Hy2.Pid(),
		},
		"panel": rt.Syncer.Status(),
	}
}

// CoreInfo 核心二进制信息（版本 + 安装状态）
func (m *Manager) CoreInfo(kind string) map[string]any {
	path := m.globalCfg().System.XrayPath
	if kind == "hysteria" {
		path = m.global.System.HysteriaPath
	}
	installed := false
	if _, err := os.Stat(path); err == nil {
		installed = true
	}
	ver := ""
	if installed {
		out, err := exec.Command(path, "version").Output()
		if err == nil {
			// 提取版本号：匹配 v?数字.数字.数字
			m := versionRe.FindStringSubmatch(string(out))
			if len(m) > 1 {
				ver = m[1]
			} else if kind == "xray" {
				ver = strings.Fields(strings.SplitN(string(out), "\n", 2)[0])[1]
			}
		}
	}
	return map[string]any{
		"installed": installed,
		"version":   ver,
		"path":      path,
	}
}

// UpdateCore 下载并更新核心二进制（nodex release 统一托管）
// 下载到数据目录 /etc/nodex/bin/ 并持久化配置路径（Docker 容器重启后内核保留）
func (m *Manager) UpdateCore(kind string) (string, error) {
	if kind != "xray" && kind != "hysteria" {
		return "", fmt.Errorf("未知核心类型: %s", kind)
	}
	// 停止全部节点（核心文件在被使用）
	m.StopAll()
	defer func() {
		m.StartAll()
	}()

	url := fmt.Sprintf("https://github.com/wooxi/nodex/releases/latest/download/%s-linux-amd64", kind)
	// 持久化内核目录（Docker volume 挂载的数据目录）
	binDir := filepath.Join(m.globalCfg().System.DataDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("创建内核目录失败: %v", err)
	}
	tmp := filepath.Join(binDir, ".nodex-core-"+kind)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("下载失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		return "", fmt.Errorf("读取下载内容失败: %v", err)
	}
	if len(data) < 4 || string(data[:4]) == "404 " || string(data[:9]) == "Not Found" {
		return "", fmt.Errorf("下载内容无效（可能版本不存在）")
	}
	// ELF 校验
	if len(data) < 4 || data[0] != 0x7f || data[1] != 'E' || data[2] != 'L' || data[3] != 'F' {
		return "", fmt.Errorf("下载内容不是有效的可执行文件")
	}
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return "", fmt.Errorf("写入失败: %v", err)
	}
	path := filepath.Join(binDir, kind)
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("替换失败: %v", err)
	}
	os.Chmod(path, 0o755)

	// 持久化内核路径到配置（Docker 重启后 /etc/nodex 卷保留内核与路径）
	cfg := m.globalCfg()
	newCfg := &config.Config{}
	if data, err := json.Marshal(cfg); err == nil {
		if err := json.Unmarshal(data, newCfg); err == nil {
			if kind == "xray" {
				newCfg.System.XrayPath = path
			} else {
				newCfg.System.HysteriaPath = path
			}
			newCfg.Save(m.cfgPath)
			m.setGlobal(newCfg)
			m.Rebuild(newCfg)
		}
	}
	info := m.CoreInfo(kind)
	return info["version"].(string), nil
}

// Users 单节点用户流量（xray + hy2 合并）
func (rt *Runtime) Users(ctx context.Context) []map[string]any {
	traffic, err := rt.Stats.GetTraffic(ctx)
	if err != nil {
		log.Printf("[nodex][%s] xray stats: %v", rt.Cfg.ID, err)
		traffic = map[int64]xray.Traffic{}
	}
	hy2t, err := rt.Hy2.FetchTraffic(ctx)
	if err == nil {
		for uid, t := range hy2t {
			if v, ok := traffic[uid]; ok {
				v.Up += t.Up
				v.Down += t.Down
				traffic[uid] = v
			} else {
				traffic[uid] = t
			}
		}
	}
	alive := rt.Syncer.AliveUsers()
	out := []map[string]any{}
	for uid, v := range traffic {
		out = append(out, map[string]any{
			"uid": uid, "up": v.Up, "down": v.Down, "traffic": v.Up + v.Down,
			"ips": alive[uid],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["traffic"].(int64) > out[j]["traffic"].(int64)
	})
	return out
}

// ResetStats 重置节点流量快照
func (rt *Runtime) ResetStats() {
	rt.Stats.Reset()
	rt.Syncer.ResetHy2()
}
