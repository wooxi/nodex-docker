package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wooxi/nodex/internal/config"
	"github.com/wooxi/nodex/internal/xray"
)

// Client Xboard/V2Board 面板对接客户端
type Client struct {
	cfg    *config.PanelConfig
	client *http.Client
}

// Config 面板返回的节点配置
// 面板模式：端口/网络/TLS 等以面板返回为准，本地配置提供 Reality 私钥等补充
// 注意：V2Board 的 vless 节点常配 ws + CDN（tls=1, network=ws）
type Config struct {
	Protocol        string          `json:"protocol"`
	ListenIP        string          `json:"listen_ip"`
	ServerPort      int             `json:"server_port"`
	Network         string          `json:"network"`
	NetworkSettings json.RawMessage `json:"networkSettings"`
	Flow            string          `json:"flow"`
	TLS             int             `json:"tls"`
	Cipher          string          `json:"cipher"`

	BaseConfig struct {
		PushInterval int `json:"push_interval"`
		PullInterval int `json:"pull_interval"`
	} `json:"base_config"`
}

// UserResp 面板返回的用户列表
type UserResp struct {
	Users []xray.User `json:"users"`
}

// UniProxyResp 通用响应
type UniProxyResp struct {
	Data json.RawMessage `json:"data"`
}

func NewClient(cfg *config.PanelConfig) *Client {
	return &Client{
		cfg: cfg,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// NewClientForNode 节点级面板客户端（URL/密钥取全局，node_id/node_type 取节点）
func NewClientForNode(global *config.PanelConfig, node *config.Node) *Client {
	cfg := *global
	cfg.NodeID = node.NodeID
	cfg.NodeType = node.NodeType
	return NewClient(&cfg)
}

func (c *Client) base() string {
	return strings.TrimRight(c.cfg.URL, "/") + "/api/v1/server/UniProxy"
}

// query 构造带 token/node_id 的请求
func (c *Client) query(ctx context.Context, method, path string, body any) ([]byte, error) {
	u := c.base() + path
	params := url.Values{}
	params.Set("token", c.cfg.Token)
	params.Set("node_id", fmt.Sprintf("%d", c.cfg.NodeID))
	if c.cfg.NodeType != "" {
		params.Set("node_type", c.cfg.NodeType)
	}
	u += "?" + params.Encode()

	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求面板失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("面板返回 %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	// 面板错误包装
	var fail struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &fail) == nil && fail.Status == "fail" {
		return nil, fmt.Errorf("面板错误: %s", fail.Message)
	}
	return data, nil
}

// FetchConfig 拉取节点配置
func (c *Client) FetchConfig(ctx context.Context) (*Config, error) {
	data, err := c.query(ctx, http.MethodGet, "/config", nil)
	if err != nil {
		return nil, err
	}
	// 面板返回格式：顶层直接是节点配置字段（无 data 包装）
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析节点配置失败: %w", err)
	}
	return &cfg, nil
}

// FetchUsers 拉取用户列表
func (c *Client) FetchUsers(ctx context.Context) ([]xray.User, error) {
	data, err := c.query(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return nil, err
	}
	var resp UserResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("解析用户列表失败: %w", err)
	}
	return resp.Users, nil
}

// PushTraffic 推送流量增量，data: [uid -> [upload, download]]（Xboard 格式）
func (c *Client) PushTraffic(ctx context.Context, data map[int64][2]int64) error {
	if len(data) == 0 {
		return nil
	}
	// Xboard 格式: {uid: [upload, download]}
	payload := map[string][]int64{}
	for uid, v := range data {
		payload[fmt.Sprintf("%d", uid)] = []int64{v[0], v[1]}
	}
	_, err := c.query(ctx, http.MethodPost, "/push", payload)
	return err
}

// PushAlive 推送在线设备 {uid: [ip,...]}
func (c *Client) PushAlive(ctx context.Context, alive map[int64][]string) error {
	if len(alive) == 0 {
		return nil
	}
	payload := map[string][]string{}
	for uid, ips := range alive {
		payload[fmt.Sprintf("%d", uid)] = ips
	}
	_, err := c.query(ctx, http.MethodPost, "/alive", payload)
	return err
}

// PushStatus 推送服务器状态
func (c *Client) PushStatus(ctx context.Context, status map[string]any) error {
	_, err := c.query(ctx, http.MethodPost, "/status", status)
	return err
}

// Test 测试面板连通性（返回拉到的节点配置摘要）
func (c *Client) Test(ctx context.Context) (string, error) {
	cfg, err := c.FetchConfig(ctx)
	if err != nil {
		return "", err
	}
	users, err := c.FetchUsers(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("连接成功：协议=%s 端口=%d 用户数=%d", cfg.Protocol, cfg.ServerPort, len(users)), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
