package panel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"

	"github.com/wooxi/nodex/internal/config"
	"github.com/wooxi/nodex/internal/xray"
)

// Syncer 节点同步器：定时拉配置/用户，推送流量/心跳/状态
// 每个节点独立实例。流量来源：xray gRPC stats + hysteria2 /traffic API，合并后按 Xboard 格式推送
//   push 格式: {uid: [upload, download]}
type Syncer struct {
	node      *config.Node
	global    *config.Config
	nodeDir   string
	panel     *Client
	xm        *xray.Manager
	hy2       *xray.Hy2Manager
	stats     *xray.StatsCollector
	accessLog *AccessLog

	hy2Last map[int64]xray.Traffic // hysteria2 流量快照
	hy2Port int                    // hysteria2 面板端口（指纹计算用）

	remotePull int // 面板 base_config 返回的间隔（覆盖本地）
	remotePush int

	lastFingerprint string // 最近应用的配置指纹

	mu       sync.Mutex
	running  bool
	stopCh   chan struct{}
	lastSync time.Time
	lastPush time.Time
	lastErr  string
}

func NewSyncer(node *config.Node, global *config.Config, nodeDir string, xm *xray.Manager, hy2 *xray.Hy2Manager, stats *xray.StatsCollector) *Syncer {
	return &Syncer{
		node:      node,
		global:    global,
		nodeDir:   nodeDir,
		panel:     NewClientForNode(&global.Panel, node),
		xm:        xm,
		hy2:       hy2,
		stats:     stats,
		accessLog: NewAccessLog(nodeDir),
		hy2Last:   map[int64]xray.Traffic{},
	}
}

func (s *Syncer) UpdateConfig(node *config.Node, global *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.node = node
	s.global = global
	s.panel = NewClientForNode(&global.Panel, node)
	s.accessLog.UpdateConfig(nodeDirOf(node, global))
	s.xm.UpdateConfig(node, global)
	s.hy2.UpdateConfig(node, global)
}

func nodeDirOf(node *config.Node, global *config.Config) string {
	return global.NodeDataDir(node)
}

// Start 启动后台同步循环
func (s *Syncer) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	ch := s.stopCh
	s.mu.Unlock()

	go func() {
		log.Println("[nodex] 同步器已启动")
		// 等待 xray stats API 就绪后首次同步（最多 8 秒）
		for i := 0; i < 4; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := s.stats.GetTraffic(ctx)
			cancel()
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		s.syncOnce()
		lastTick := time.Now()
		for {
			select {
			case <-ch:
				log.Println("[nodex] 同步器已停止")
				return
			case <-time.After(s.loopInterval()):
				// 防漂移：以整周期节奏运行，避免 time.After 累积误差
				if time.Since(lastTick) < s.loopInterval()/2 {
					continue
				}
				lastTick = time.Now()
				s.syncOnce()
			}
		}
	}()
}

// loopInterval 同步循环周期：面板 base_config 优先，其次本地 pull_interval
func (s *Syncer) loopInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.remotePull
	if sec <= 0 {
		sec = s.global.Panel.PullInterval
	}
	if sec <= 0 {
		sec = 60
	}
	if sec < 10 {
		sec = 10 // 下限 10s，防止面板误配导致刷爆
	}
	return time.Duration(sec) * time.Second
}

// pushDue 是否到达推送时机（流量/在线/状态上报受 PushInterval 节流）
func (s *Syncer) pushDue() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.remotePush
	if sec <= 0 {
		sec = s.global.Panel.PushInterval
	}
	if sec <= 0 {
		sec = 60
	}
	now := time.Now()
	if now.Sub(s.lastPush) >= time.Duration(sec)*time.Second {
		s.lastPush = now
		return true
	}
	return false
}

func (s *Syncer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
}

// Status 同步器状态
func (s *Syncer) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"running":  s.running,
		"lastSync": s.lastSync.Format("2006-01-02 15:04:05"),
		"lastError": s.lastErr,
	}
}

// syncOnce 执行一轮完整同步
func (s *Syncer) syncOnce() {
	s.mu.Lock()
	node := s.node
	global := s.global
	s.mu.Unlock()

	if !global.Panel.Enabled {
		return
	}

	ctx := context.Background()

	// 1. 拉取节点配置（端口/网络/TLS 等，本地覆盖为辅）
	remote, err := s.panel.FetchConfig(ctx)
	if err != nil {
		s.setErr("拉取节点配置失败: " + err.Error())
		return
	}
	// 面板 base_config 返回的间隔覆盖本地（同步循环节奏）
	s.mu.Lock()
	if remote.BaseConfig.PullInterval > 0 {
		s.remotePull = remote.BaseConfig.PullInterval
	}
	if remote.BaseConfig.PushInterval > 0 {
		s.remotePush = remote.BaseConfig.PushInterval
	}
	s.mu.Unlock()
	// 解析 ws/grpc 传输参数并注入 xray
	remoteCfg := &xray.RemoteConfig{
		Protocol: remote.Protocol,
		Port:     remote.ServerPort,
		Network:  remote.Network,
		TLS:      remote.TLS,
		Flow:     remote.Flow,
		Cipher:   remote.Cipher,
	}
	if len(remote.NetworkSettings) > 0 {
		var ns struct {
			Path string `json:"path"`
			Host string `json:"host"`
		}
		if err := json.Unmarshal(remote.NetworkSettings, &ns); err == nil {
			remoteCfg.WSPath = ns.Path
			remoteCfg.WSHost = ns.Host
		}
	}
	s.xm.SetRemoteConfig(remoteCfg)
	// 2. 拉取用户列表并注入 xray 与 hysteria2
	users, err := s.panel.FetchUsers(ctx)
	if err != nil {
		s.setErr("拉取用户列表失败: " + err.Error())
		return
	}
	s.xm.SetUsers(users)
	s.hy2.SetUsers(users)
	s.accessLog.SetUsers(users)
	if len(users) == 0 {
		s.setErr("面板返回的用户列表为空（节点未关联任何用户组）")
		return
	}

	// 3. 若面板指定了协议且本地未显式覆盖，采用面板协议
	if remote.Protocol != "" && node.NodeType == "" {
		node.NodeType = remote.Protocol
		s.xm.UpdateConfig(node, global)
	}
	// hysteria 节点：hy2 端口跟随面板
	if (remote.Protocol == "hysteria" || remote.Protocol == "hysteria2") && remote.ServerPort > 0 {
		s.hy2.SetRemotePort(remote.ServerPort)
		s.hy2Port = remote.ServerPort
	} else {
		s.hy2.SetRemotePort(0)
		s.hy2Port = 0
	}

	// 3.5 配置指纹变化时重启 xray + hy2（使 remote/users 生效）
	// users 排序后进指纹：面板返回顺序抖动不触发无谓重启
	sortedUsers := append([]xray.User(nil), users...)
	sort.Slice(sortedUsers, func(i, j int) bool { return sortedUsers[i].ID < sortedUsers[j].ID })
	fingerprint := fmt.Sprintf("%s|%d|%s|%d|%v|%d",
		remoteCfg.Protocol, remoteCfg.Port, remoteCfg.Network, remoteCfg.TLS, sortedUsers, s.hy2Port)
	if fingerprint != s.lastFingerprint {
		s.lastFingerprint = fingerprint
		if err := s.xm.Restart(); err != nil {
			s.setErr("重启 xray 应用配置失败: " + err.Error())
			return
		}
		if s.hy2.IsRunning() {
			s.hy2.Stop()
			if err := s.hy2.Start(); err != nil {
				s.setErr("重启 hysteria2 失败: " + err.Error())
				return
			}
		}
		s.stats.Reset()
		s.ResetHy2()
		log.Printf("[nodex] 面板配置已应用（端口=%d 网络=%s 用户=%d）", remoteCfg.Port, remoteCfg.Network, len(users))
	}

	// 4-6. 推送（流量/在线/状态）受 PushInterval 节流；快照仅在推送时推进，
	//      未到推送周期时增量自动累积，不会丢失
	if s.pushDue() {
		// 4. 推送流量增量（xray + hysteria2 合并）
		diffs := map[int64]xray.Traffic{}
		xdiff, err := s.stats.SnapshotAndDiff(ctx)
		if err != nil {
			s.setErr("读取 xray 流量统计失败: " + err.Error())
			return
		}
		for uid, d := range xdiff {
			diffs[uid] = d
		}
		hdiff, err := s.hy2.SnapshotAndDiff(ctx, &s.hy2Last)
		if err == nil {
			for uid, d := range hdiff {
				if v, ok := diffs[uid]; ok {
					v.Up += d.Up
					v.Down += d.Down
					diffs[uid] = v
				} else {
					diffs[uid] = d
				}
			}
		}
		if len(diffs) > 0 {
			// 转成 Xboard push 格式 {uid: [upload, download]}
			payload := map[int64][2]int64{}
			for uid, d := range diffs {
				payload[uid] = [2]int64{d.Up, d.Down}
			}
			if err := s.panel.PushTraffic(ctx, payload); err != nil {
				s.setErr("推送流量失败: " + err.Error())
				return
			}
		}

		// 5. 推送在线设备
		if alive := s.accessLog.AliveIPs(3 * time.Minute); len(alive) > 0 {
			if err := s.panel.PushAlive(ctx, alive); err != nil {
				s.setErr("推送在线设备失败: " + err.Error())
				return
			}
		}

		// 6. 推送服务器状态
		if status := s.collectStatus(); status != nil {
			if err := s.panel.PushStatus(ctx, status); err != nil {
				// 状态推送失败不影响主流程
				log.Printf("[nodex] 状态推送失败: %v", err)
			}
		}
	}

	s.mu.Lock()
	s.lastSync = time.Now()
	s.lastErr = ""
	s.mu.Unlock()
	log.Printf("[nodex] 同步完成：用户 %d 个", len(users))
}

func (s *Syncer) setErr(msg string) {
	s.mu.Lock()
	s.lastErr = msg
	s.lastSync = time.Now()
	s.mu.Unlock()
	log.Printf("[nodex] %s", msg)
}

// collectStatus 收集系统状态（Xboard /status 协议）
func (s *Syncer) collectStatus() map[string]any {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return nil
	}
	cp, err := cpu.Percent(0, false)
	if err != nil || len(cp) == 0 {
		cp = []float64{0}
	}
	du, err := disk.Usage("/")
	if err != nil {
		return nil
	}
	sw, _ := mem.SwapMemory()
	if sw == nil {
		sw = &mem.SwapMemoryStat{}
	}
	return map[string]any{
		"cpu": int(cp[0]),
		"mem": map[string]any{
			"total": int(vm.Total),
			"used":  int(vm.Used),
		},
		"swap": map[string]any{
			"total": int(sw.Total),
			"used":  int(sw.Used),
		},
		"disk": map[string]any{
			"total": int(du.Total),
			"used":  int(du.Used),
		},
	}
}

// AliveUsers 返回最近活跃的 uid -> ip 列表（供 Web UI 展示）
func (s *Syncer) AliveUsers() map[int64][]string {
	return s.accessLog.AliveIPs(10 * time.Minute)
}

// Hy2Secret 公开 hysteria traffic API 密钥（供 web 层启动 hy2 用）
func (s *Syncer) Hy2Secret() string {
	return s.hy2Secret()
}

// ResetHy2 清空 hysteria2 流量快照（hy2 重启后调用）
func (s *Syncer) ResetHy2() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hy2Last = map[int64]xray.Traffic{}
}

// hy2Secret 生成稳定的 hysteria traffic API 密钥（按节点派生，重启不变）
func (s *Syncer) hy2Secret() string {
	h := sha256.Sum256([]byte("nodex-hy2:" + s.global.System.DataDir + ":" + s.node.ID + ":" + s.global.Web.Password))
	return hex.EncodeToString(h[:16])
}

var (
	addrRe = regexp.MustCompile(`"addr":\s*"([^"]+)"`)
	uidRe  = regexp.MustCompile(`"id":\s*"(\d+)"`)
)

// AccessLog 解析 xray access log 与 hysteria 日志提取在线设备（uid -> IP 集合）
type AccessLog struct {
	mu        sync.Mutex
	path      string // xray access log
	hy2Path   string // hysteria 日志
	offsets   map[string]int64 // 文件 -> 上次解析偏移
	alive     map[int64]map[string]time.Time // uid -> ip -> 最后活跃时间
	users     map[int64]string               // uid -> uuid
}

func NewAccessLog(nodeDir string) *AccessLog {
	return &AccessLog{
		path:    filepath.Join(nodeDir, "xray", "access.log"),
		hy2Path: filepath.Join(nodeDir, "hy2", "hy2.log"),
		offsets: map[string]int64{},
		alive:   map[int64]map[string]time.Time{},
		users:   map[int64]string{},
	}
}

func (a *AccessLog) UpdateConfig(nodeDir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.path = filepath.Join(nodeDir, "xray", "access.log")
	a.hy2Path = filepath.Join(nodeDir, "hy2", "hy2.log")
}

func (a *AccessLog) SetUsers(users []xray.User) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.users = map[int64]string{}
	for _, u := range users {
		a.users[u.ID] = u.UUID
	}
}

// Parse 增量解析日志（xray access + hysteria，每次只读新增部分）
func (a *AccessLog) Parse() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()

	// xray access log
	if data := a.readNew(a.path); len(data) > 0 {
		for _, line := range strings.Split(data, "\n") {
			if !strings.Contains(line, "from ") || !strings.Contains(line, "accepted ") {
				continue
			}
			parts := strings.Fields(line)
			var ip string
			for i, p := range parts {
				if p == "from" && i+1 < len(parts) {
					ip = strings.Split(parts[i+1], ":")[0]
					break
				}
			}
			uid := a.findUID(line)
			if ip == "" || uid == 0 {
				continue
			}
			if a.alive[uid] == nil {
				a.alive[uid] = map[string]time.Time{}
			}
			a.alive[uid][ip] = now
		}
	}

	// hysteria 日志: client connected {"addr": "1.2.3.4:port", "id": "1", ...}
	if data := a.readNew(a.hy2Path); len(data) > 0 {
		for _, line := range strings.Split(data, "\n") {
			idx := strings.Index(line, "client connected")
			disconnected := false
			if idx < 0 {
				idx = strings.Index(line, "client disconnected")
				if idx < 0 {
					continue
				}
				disconnected = true
			}
			// addr（可能带端口，需剥离）
			ip := ""
			if m := addrRe.FindStringSubmatch(line); len(m) > 1 {
				addr := m[1]
				if strings.HasPrefix(addr, "[") { // [ipv6]:port
					if end := strings.Index(addr, "]"); end > 0 {
						ip = addr[1:end]
					}
				} else if idx := strings.LastIndex(addr, ":"); idx > 0 {
					ip = addr[:idx] // ipv4:port
				} else {
					ip = addr
				}
			}
			uid := int64(0)
			if m := uidRe.FindStringSubmatch(line); len(m) > 1 {
				uid, _ = strconv.ParseInt(m[1], 10, 64)
			}
			if ip == "" || uid == 0 {
				continue
			}
			if a.alive[uid] == nil {
				a.alive[uid] = map[string]time.Time{}
			}
			if disconnected {
				delete(a.alive[uid], ip)
			} else {
				a.alive[uid][ip] = now
			}
		}
	}
}

// readNew 读取文件新增部分（返回新增内容并更新偏移）
func (a *AccessLog) readNew(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	off := a.offsets[path]
	if st, err := f.Stat(); err == nil && st.Size() < off {
		off = 0 // 日志被轮转/截断
	}
	if _, err := f.Seek(off, 0); err != nil {
		return ""
	}
	buf := make([]byte, 256*1024)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return ""
	}
	a.offsets[path] = off + int64(n)
	return string(buf[:n])
}

// findUID 从日志行提取 uid
// 新格式: ... accepted tcp:... [in-main >> direct] email: 1@nodex
// 旧格式: ... accepted tcp:... [1@nodex] / [email@...]
func (a *AccessLog) findUID(line string) int64 {
	// 新格式：email: uid@nodex
	if idx := strings.Index(line, "email: "); idx >= 0 {
		email := strings.TrimSpace(line[idx+len("email: "):])
		if uid, ok := xray.ParseEmail(email); ok {
			return uid
		}
	}
	// 旧格式：[uid@nodex]
	idx := strings.Index(line, "[")
	if idx < 0 {
		return 0
	}
	end := strings.Index(line[idx:], "]")
	if end < 0 {
		return 0
	}
	email := line[idx+1 : idx+end]
	if uid, ok := xray.ParseEmail(email); ok {
		return uid
	}
	// 兼容面板直发的 email（如 uuid@uuid）
	for uid, uuid := range a.users {
		if strings.Contains(email, uuid) {
			return uid
		}
	}
	return 0
}

// AliveIPs 返回最近 window 内活跃的 uid -> ip 列表
func (a *AccessLog) AliveIPs(window time.Duration) map[int64][]string {
	a.Parse()
	a.mu.Lock()
	defer a.mu.Unlock()

	cutoff := time.Now().Add(-window)
	out := map[int64][]string{}
	for uid, ips := range a.alive {
		var list []string
		for ip, t := range ips {
			if t.After(cutoff) {
				list = append(list, ip)
			}
		}
		if len(list) > 0 {
			out[uid] = list
		}
	}
	return out
}
