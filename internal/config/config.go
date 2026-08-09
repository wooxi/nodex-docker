package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config 是 NodeX 的完整配置，由 Web 前端表单生成，用户不直接编辑 JSON。
// 多节点架构：system/web 为全局配置，nodes 为节点列表（每个节点独立面板对接与协议配置）。
type Config struct {
	Web    WebConfig    `json:"web"`
	Panel  PanelConfig  `json:"panel"` // 全局面板对接（一个部署只对接一个面板）
	System SystemConfig `json:"system"`
	Nodes  []*Node      `json:"nodes"`
}

type WebConfig struct {
	Port       int    `json:"port"`       // Web 管理端口
	Listen     string `json:"listen"`     // 监听地址（默认 0.0.0.0；OpenWrt LuCI 模式用 127.0.0.1）
	Password   string `json:"password"`   // 管理密码（bcrypt hash）
	AllowLocal bool   `json:"allow_local"` // 允许本机回环免认证（LuCI 代理访问）
}

type SystemConfig struct {
	XrayPath      string `json:"xray_path"`      // xray 可执行文件路径
	HysteriaPath  string `json:"hysteria_path"`  // hysteria 可执行文件路径
	LogLevel      string `json:"log_level"`      // debug|info|warning|error
	DataDir       string `json:"data_dir"`       // 数据目录（配置/xray 配置/日志）
	CertPath      string `json:"cert_path"`      // hysteria2 默认证书路径
	KeyPath       string `json:"key_path"`       // hysteria2 默认私钥路径
	APIPortBase   int    `json:"api_port_base"`  // xray gRPC API 起始端口（每节点 +1）
	Hy2APIPortBase int   `json:"hy2_api_port_base"` // hysteria traffic API 起始端口
}

// Node 单个节点配置
// 面板对接：URL/密钥为全局（Config.Panel），node_id/node_type 每节点独立
type Node struct {
	ID       string     `json:"id"`       // 唯一标识（如 n1）
	Name     string     `json:"name"`     // 显示名称
	Enabled  bool       `json:"enabled"`  // 是否启用
	NodeID   int        `json:"node_id"`  // 面板节点 ID（每节点不同）
	NodeType string     `json:"node_type"` // 面板节点类型，留空自动
	Node     NodeConfig `json:"node"`     // 协议配置
	Forward  Forward    `json:"forward"`  // 转发出站（XrayR 转发模式）
}

// Forward 转发出站配置：面板入站流量转发到落地节点（vless+ws+tls）
type Forward struct {
	Enabled     bool          `json:"enabled"`      // 启用转发（替代直连）
	Targets     []ForwardTarget `json:"targets"`    // 目标服务器（负载均衡）
	UUID        string        `json:"uuid"`         // 落地节点 UUID
	ServerName  string        `json:"server_name"`  // SNI
	WSPath      string        `json:"ws_path"`      // WebSocket 路径
	WSHost      string        `json:"ws_host"`      // WebSocket Host 头
	Fingerprint string        `json:"fingerprint"`  // 指纹，默认 chrome
}

// ForwardTarget 转发目标服务器
type ForwardTarget struct {
	Address string `json:"address"` // IP 或域名
	Port    int    `json:"port"`    // 端口，默认 443
	Weight  int    `json:"weight"`  // 负载权重，默认 1
}

type PanelConfig struct {
	Enabled      bool   `json:"enabled"`       // 是否启用面板对接
	URL          string `json:"url"`           // 面板地址
	Token        string `json:"token"`         // 面板通信密钥
	NodeID       int    `json:"node_id"`       // 面板节点 ID
	NodeType     string `json:"node_type"`     // 节点类型，留空由面板返回决定
	PullInterval int    `json:"pull_interval"` // 拉取间隔（秒）
	PushInterval int    `json:"push_interval"` // 上报间隔（秒）
}

type NodeConfig struct {
	Protocol string `json:"protocol"` // vless | vmess | trojan | shadowsocks | hysteria2
	Port     int    `json:"port"`     // 监听端口（面板模式下面板端口优先）
	UUID     string `json:"uuid"`     // 本地模式用户 ID

	TLS        int     `json:"tls"` // 0=关闭 1=TLS 2=Reality（面板模式以面板为准）
	CertPath   string  `json:"cert_path"`
	KeyPath    string  `json:"key_path"`
	ServerName string  `json:"server_name"`
	Reality    Reality `json:"reality"`

	Hy2      Hy2    `json:"hy2"`
	SSMethod string `json:"ss_method"`
}

type Reality struct {
	Dest        string `json:"dest"`
	ServerNames string `json:"server_names"`
	PrivateKey  string `json:"private_key"`
	ShortIDs    string `json:"short_ids"`
	PublicKey   string `json:"public_key"` // 只读展示
}

type Hy2 struct {
	Port         int    `json:"port"`
	Password     string `json:"password"`
	Obfs         string `json:"obfs"`
	ObfsPassword string `json:"obfs_password"`
	UpMbps       int    `json:"up_mbps"`
	DownMbps     int    `json:"down_mbps"`
	IgnoreBW     bool   `json:"ignore_bw"`
	CertPath     string `json:"cert_path"`
	KeyPath      string `json:"key_path"`
	BinPath      string `json:"bin_path,omitempty"` // 兼容旧版字段
}

const DefaultConfigPath = "/etc/nodex/config.json"

func Default() *Config {
	return &Config{
		Web: WebConfig{Port: 8888},
		Panel: PanelConfig{
			PullInterval: 60,
			PushInterval: 60,
		},
		System: SystemConfig{
			XrayPath:       "/usr/bin/xray",
			HysteriaPath:   "/usr/bin/hysteria",
			LogLevel:       "info",
			DataDir:        "/etc/nodex",
			APIPortBase:    10085,
			Hy2APIPortBase: 8444,
		},
		Nodes: []*Node{},
	}
}

// DefaultNode 返回默认节点配置（新节点模板）
func DefaultNode() *Node {
	return &Node{
		ID:      newID(),
		Name:    "新节点",
		Enabled: true,
		Forward: Forward{Fingerprint: "chrome"},
		Node: NodeConfig{
			Protocol: "vless",
			Port:     8686,
			TLS:      0,
			SSMethod: "2022-blake3-aes-128-gcm",
			Reality: Reality{
				Dest:        "www.amazon.com:443",
				ServerNames: "www.amazon.com",
			},
			Hy2: Hy2{
				Port:     9443,
				Obfs:     "none",
				UpMbps:   100,
				DownMbps: 1000,
			},
		},
	}
}

func (c *Config) DataDir() string {
	if c.System.DataDir == "" {
		return "/etc/nodex"
	}
	return c.System.DataDir
}

// NodeDataDir 节点数据目录（每节点独立）
func (c *Config) NodeDataDir(n *Node) string {
	return filepath.Join(c.DataDir(), "nodes", n.ID)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// 兼容 v0.2.x：node.Panel（每节点面板）与全局 panel.node_id 迁移到节点级 NodeID/NodeType
	type legacyNode struct {
		ID      string      `json:"id"`
		Name    string      `json:"name"`
		Enabled bool        `json:"enabled"`
		Panel   PanelConfig `json:"panel"`
		Node    NodeConfig  `json:"node"`
	}
	var legacyNodes struct {
		Panel PanelConfig   `json:"panel"`
		Nodes []legacyNode  `json:"nodes"`
	}
	if err := json.Unmarshal(data, &legacyNodes); err == nil && len(legacyNodes.Nodes) > 0 {
		if cfg.Panel.URL == "" && legacyNodes.Panel.URL != "" {
			cfg.Panel = legacyNodes.Panel
		} else if cfg.Panel.URL == "" && legacyNodes.Nodes[0].Panel.URL != "" {
			cfg.Panel = legacyNodes.Nodes[0].Panel
		}
		// 节点级 node_id/node_type：优先节点自带，其次全局
		for i, ln := range legacyNodes.Nodes {
			n := cfg.Nodes[i]
			if n.NodeID == 0 && ln.Panel.NodeID > 0 {
				n.NodeID = ln.Panel.NodeID
			}
			if n.NodeType == "" && ln.Panel.NodeType != "" {
				n.NodeType = ln.Panel.NodeType
			}
		}
		if cfg.Panel.NodeID > 0 {
			for _, n := range cfg.Nodes {
				if n.NodeID == 0 {
					n.NodeID = cfg.Panel.NodeID
				}
			}
			cfg.Panel.NodeID = 0
			cfg.Panel.NodeType = ""
		}
	}
	// 兼容旧版单节点配置：迁移到 nodes[0] + 全局 panel
	if len(cfg.Nodes) == 0 {
		var legacy struct {
			Web    WebConfig    `json:"web"`
			System SystemConfig `json:"system"`
			Panel  PanelConfig  `json:"panel"`
			Node   NodeConfig   `json:"node"`
		}
		if err := json.Unmarshal(data, &legacy); err == nil {
			if legacy.Panel.URL != "" || legacy.Node.Port != 0 || legacy.Node.Protocol != "" {
				cfg.Web = legacy.Web
				cfg.System = legacy.System
				cfg.Panel = legacy.Panel
				// 兼容旧字段：hysteria 二进制路径在 Hy2.BinPath
				if cfg.System.HysteriaPath == "" {
					cfg.System.HysteriaPath = legacy.Node.Hy2.BinPath
				}
				if cfg.System.HysteriaPath == "" {
					cfg.System.HysteriaPath = "/usr/bin/hysteria"
				}
				// 证书路径迁移
				if cfg.System.CertPath == "" {
					cfg.System.CertPath = legacy.Node.Hy2.CertPath
					cfg.System.KeyPath = legacy.Node.Hy2.KeyPath
				}
				if cfg.System.APIPortBase == 0 {
					cfg.System.APIPortBase = 10085
				}
				if cfg.System.Hy2APIPortBase == 0 {
					cfg.System.Hy2APIPortBase = 8444
				}
				n := DefaultNode()
				n.ID = "n1"
				n.Name = "节点1"
				n.Node = legacy.Node
				cfg.Nodes = []*Node{n}
			}
		}
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (c *Config) ConfigPath() string { return DefaultConfigPath }

// Validate 校验配置
func (c *Config) Validate() error {
	if c.Web.Port < 1 || c.Web.Port > 65535 {
		return errors.New("Web 管理端口无效")
	}
	if c.Panel.Enabled {
		if c.Panel.URL == "" {
			return errors.New("面板地址不能为空")
		}
		if c.Panel.Token == "" {
			return errors.New("通信密钥不能为空")
		}
	}
	if len(c.Nodes) == 0 {
		return errors.New("至少需要配置一个节点")
	}
	seen := map[string]bool{}
	seenNodeID := map[int]string{}
	for _, n := range c.Nodes {
		if n.ID == "" {
			return errors.New("节点 ID 不能为空")
		}
		if seen[n.ID] {
			return errors.New("节点 ID 重复: " + n.ID)
		}
		seen[n.ID] = true
		if c.Panel.Enabled && n.NodeID <= 0 {
			return errors.New("节点 [" + n.Name + "] 面板节点 ID 必须大于 0")
		}
		if n.NodeID > 0 {
			if prev, ok := seenNodeID[n.NodeID]; ok {
				return fmt.Errorf("节点 [%s] 与 [%s] 的面板节点 ID 重复: %d", n.Name, prev, n.NodeID)
			}
			seenNodeID[n.NodeID] = n.Name
		}
		switch n.Node.Protocol {
		case "vless", "vmess", "trojan", "shadowsocks", "hysteria2":
		default:
			return errors.New("节点 [" + n.Name + "] 不支持的协议: " + n.Node.Protocol)
		}
		if n.Node.Port < 1 || n.Node.Port > 65535 {
			return errors.New("节点 [" + n.Name + "] 端口无效")
		}
		// Trojan 必须 TLS：EnsureDefaults 已强制 tls=1，这里保留证书必填校验
		if n.Node.Protocol == "trojan" && n.Node.CertPath == "" {
			return errors.New("节点 [" + n.Name + "] Trojan 协议必须填写证书路径")
		}
	}
	return nil
}

// EnsureDefaults 填充空字段默认值
func (c *Config) EnsureDefaults() {
	for _, n := range c.Nodes {
		if n.Node.UUID == "" {
			n.Node.UUID = newUUID()
		}
		if n.Node.Hy2.Password == "" {
			n.Node.Hy2.Password = randHex(16)
		}
		if n.Node.Hy2.ObfsPassword == "" && n.Node.Hy2.Obfs == "salamander" {
			n.Node.Hy2.ObfsPassword = randHex(8)
		}
	}
	if c.Panel.PullInterval == 0 {
		c.Panel.PullInterval = 60
	}
	if c.Panel.PushInterval == 0 {
		c.Panel.PushInterval = 60
	}
}
