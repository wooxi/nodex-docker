package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wooxi/nodex/internal/config"
	"github.com/wooxi/nodex/internal/manager"
	"github.com/wooxi/nodex/internal/panel"
	"github.com/wooxi/nodex/internal/xray"
)

//go:embed all:dist
var distFS embed.FS

// Server Web 管理服务（多节点）
type Server struct {
	cfg     *config.Config
	cfgPath string
	mgr     *manager.Manager

	mu       sync.Mutex
	sessions map[string]time.Time
}

func New(cfg *config.Config, cfgPath string, mgr *manager.Manager) *Server {
	return &Server{
		cfg:      cfg,
		cfgPath:  cfgPath,
		mgr:      mgr,
		sessions: map[string]time.Time{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/hy2-auth", s.handleHy2Auth) // hysteria2 认证回调（仅本机）
	mux.HandleFunc("/api/status", s.auth(s.handleStatus))
	mux.HandleFunc("/api/config", s.auth(s.handleConfig))
	mux.HandleFunc("/api/nodes/test", s.auth(s.handlePanelTest))
	mux.HandleFunc("/api/action", s.auth(s.handleAction))
	mux.HandleFunc("/api/core/update", s.auth(s.handleCoreUpdate))
	mux.HandleFunc("/api/core/info", s.auth(s.handleCoreInfo))
	mux.HandleFunc("/api/logs", s.auth(s.handleLogs))
	mux.HandleFunc("/api/users", s.auth(s.handleUsers))
	mux.HandleFunc("/api/generate", s.auth(s.handleGenerate))

	// 前端静态资源
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatalf("[nodex] 前端资源缺失: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := fs.Stat(sub, p); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}))

	return mux
}

// ---------- 认证 ----------

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 本机回环免认证（OpenWrt LuCI 代理模式）
		if s.cfg.Web.AllowLocal && isLocal(r.RemoteAddr) {
			next(w, r)
			return
		}
		token := r.Header.Get("X-Auth-Token")
		if token == "" {
			if c, err := r.Cookie("nodex_token"); err == nil {
				token = c.Value
			}
		}
		s.mu.Lock()
		exp, ok := s.sessions[token]
		if ok && time.Since(exp) > 24*time.Hour {
			delete(s.sessions, token)
			ok = false
		}
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "未登录或登录已过期"})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数错误"})
		return
	}
	if s.cfg.Web.Password == "" {
		if len(req.Password) < 6 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "密码至少 6 位"})
			return
		}
		hash, err := config.HashPassword(req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "密码加密失败"})
			return
		}
		s.cfg.Web.Password = hash
		s.saveConfig()
	}
	if !config.CheckPassword(s.cfg.Web.Password, req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "密码错误"})
		return
	}
	token := genToken()
	s.mu.Lock()
	s.sessions[token] = time.Now()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"token": token})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Auth-Token")
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- API ----------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	nodes := s.mgr.Status()
	totalUsers := 0
	running := 0
	for _, n := range nodes {
		if n["enabled"].(bool) {
			totalUsers++
		}
		if n["xray"].(map[string]any)["running"].(bool) {
			running++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":   nodes,
		"running": running,
		"total":   len(nodes),
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg)
	case http.MethodPut:
		var newCfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "配置格式错误"})
			return
		}
		if newCfg.Web.Password == "" {
			newCfg.Web.Password = s.cfg.Web.Password
		} else if !strings.HasPrefix(newCfg.Web.Password, "$2") {
			hash, err := config.HashPassword(newCfg.Web.Password)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "密码加密失败"})
				return
			}
			newCfg.Web.Password = hash
		}
		if err := newCfg.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		newCfg.EnsureDefaults()
		// 最小化重启：仅节点参数变化重启该节点，节点增删/系统变更才全量重启
		s.mgr.ApplyConfig(s.cfg, &newCfg)
		s.cfg = &newCfg
		s.saveConfig()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": s.cfg})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
	}
}

func (s *Server) handlePanelTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	var req struct {
		URL      string `json:"url"`
		Token    string `json:"token"`
		NodeID   int    `json:"node_id"`
		NodeType string `json:"node_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数错误"})
		return
	}
	if req.URL == "" || req.Token == "" || req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请填写面板地址、通信密钥和节点 ID"})
		return
	}
	client := panel.NewClient(&config.PanelConfig{
		URL: req.URL, Token: req.Token, NodeID: req.NodeID, NodeType: req.NodeType,
	})
	msg, err := client.Test(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	var req struct {
		Action string `json:"action"` // start|stop|restart|sync
		NodeID string `json:"node_id"` // 空 = 全部节点
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数错误"})
		return
	}
	switch req.Action {
	case "start":
		if req.NodeID != "" {
			s.mgr.Start(req.NodeID)
		} else {
			s.mgr.StartAll()
		}
	case "stop":
		if req.NodeID != "" {
			s.mgr.Stop(req.NodeID)
		} else {
			s.mgr.StopAll()
		}
	case "start-xray":
		s.mgr.StartAllXray()
	case "stop-xray":
		s.mgr.StopAllXray()
	case "start-hy2":
		s.mgr.StartAllHy2()
	case "stop-hy2":
		s.mgr.StopAllHy2()
	case "restart":
		if req.NodeID != "" {
			s.mgr.Restart(req.NodeID)
		} else {
			s.mgr.StopAll()
			time.Sleep(300 * time.Millisecond)
			s.mgr.StartAll()
		}
	case "sync":
		s.mgr.SyncAll()
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知操作"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleHy2Auth hysteria2 认证回调（auth.http 模式）
// 请求: {addr, auth, tx}  响应: {ok, id}；node 参数指定节点
func (s *Server) handleHy2Auth(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") && !strings.HasPrefix(r.RemoteAddr, "[::1]:") {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false})
		return
	}
	nodeID := r.URL.Query().Get("node")
	rt := s.mgr.Get(nodeID)
	if rt == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false})
		return
	}
	var req struct {
		Auth string `json:"auth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false})
		return
	}
	if uid, ok := rt.Hy2.AuthUser(req.Auth); ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": fmt.Sprintf("%d", uid)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": false})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node")
	rt := s.mgr.Get(nodeID)
	if rt == nil {
		writeJSON(w, http.StatusOK, map[string]any{"logs": ""})
		return
	}
	var path string
	if r.URL.Query().Get("type") == "access" {
		path = rt.Dir + "/xray/access.log"
	} else {
		path = rt.Dir + "/xray/error.log"
	}
	// 从文件尾部倒读最后 200 行（不整文件读入内存，避免路由器闪存 IO 放大）
	const (
		maxLines = 200
		maxBytes = 512 * 1024
	)
	f, err := os.Open(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"logs": ""})
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"logs": ""})
		return
	}
	start := st.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"logs": ""})
		return
	}
	lines := strings.Split(string(buf), "\n")
	if start > 0 {
		lines = lines[1:] // 丢弃首行可能的不完整片段
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": strings.Join(lines, "\n")})
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if nodeID == "" {
		// 全部节点汇总
		all := []map[string]any{}
		for _, rt := range s.mgr.Runtimes() {
			for _, u := range rt.Users(ctx) {
				u["node"] = rt.Cfg.ID
				u["node_name"] = rt.Cfg.Name
				all = append(all, u)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": all})
		return
	}
	rt := s.mgr.Get(nodeID)
	if rt == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "节点不存在"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": rt.Users(ctx)})
}

// handleCoreInfo 核心二进制信息
func (s *Server) handleCoreInfo(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("type")
	if kind == "" {
		kind = "xray"
	}
	writeJSON(w, http.StatusOK, s.mgr.CoreInfo(kind))
}

// handleCoreUpdate 下载更新核心
func (s *Server) handleCoreUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	var req struct {
		Type string `json:"type"` // xray | hysteria
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Type != "xray" && req.Type != "hysteria") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "type 必须为 xray 或 hysteria"})
		return
	}
	ver, err := s.mgr.UpdateCore(req.Type)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": ver})
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	var req struct {
		Type string `json:"type"` // uuid|password|hex|reality
		Len  int    `json:"len"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数错误"})
		return
	}
	switch req.Type {
	case "uuid":
		writeJSON(w, http.StatusOK, map[string]any{"value": config.GenUUID()})
	case "password", "hex":
		n := req.Len
		if n <= 0 {
			n = 16
		}
		writeJSON(w, http.StatusOK, map[string]any{"value": config.GenHex(n)})
	case "reality":
		priv, pub, sid, err := xray.GenRealityKeys()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"privateKey": priv, "publicKey": pub, "shortId": sid})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知类型"})
	}
}

// ---------- 工具 ----------

func (s *Server) saveConfig() {
	if err := s.cfg.Save(s.cfgPath); err != nil {
		log.Printf("[nodex] 保存配置失败: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	// 显式 Content-Length：避免 chunked 编码（LuCI nixio 代理不解 chunked）
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(`{"error":"marshal failed"}`)
		code = http.StatusInternalServerError
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(code)
	w.Write(data)
}

func isLocal(addr string) bool {
	return strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "[::1]:")
}

func genToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
