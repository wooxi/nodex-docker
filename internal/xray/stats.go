package xray

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	statsService "github.com/xtls/xray-core/app/stats/command"
)

// Traffic 用户流量（up=上行, down=下行）
type Traffic struct {
	Up   int64
	Down int64
}

// StatsCollector 通过 xray gRPC API 收集用户流量
type StatsCollector struct {
	mu     sync.Mutex
	conn   *grpc.ClientConn
	client statsService.StatsServiceClient
	// last 记录上次的累计流量（用于计算增量）
	last map[int64]Traffic
}

func NewStatsCollector() *StatsCollector {
	return &StatsCollector{last: map[int64]Traffic{}}
}

// Connect 连接 xray API（默认 127.0.0.1:10085）
func (s *StatsCollector) Connect(ctx context.Context, addr string) error {
	if addr == "" {
		addr = "127.0.0.1:10085"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	s.conn = conn
	s.client = statsService.NewStatsServiceClient(conn)
	return nil
}

func (s *StatsCollector) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}

// GetTraffic 获取所有用户的累计流量（uid -> Traffic）
// 一次 QueryStats 拉取全部 user 统计，按名称后缀区分上下行
func (s *StatsCollector) GetTraffic(ctx context.Context) (map[int64]Traffic, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("未连接 xray API")
	}

	resp, err := client.QueryStats(ctx, &statsService.QueryStatsRequest{
		Pattern: "user>>>",
		Reset_:  false,
	})
	if err != nil {
		return nil, err
	}
	out := map[int64]Traffic{}
	for _, stat := range resp.GetStat() {
		name := stat.GetName()
		if uid, ok := ParseEmail(name); ok {
			t := out[uid]
			if strings.HasSuffix(name, ">>>traffic>>>uplink") {
				t.Up += stat.GetValue()
			} else if strings.HasSuffix(name, ">>>traffic>>>downlink") {
				t.Down += stat.GetValue()
			}
			out[uid] = t
		}
	}
	return out, nil
}

// SnapshotAndDiff 计算增量：返回 uid -> 本次上行/下行增量字节，并更新快照
func (s *StatsCollector) SnapshotAndDiff(ctx context.Context) (map[int64]Traffic, error) {
	cur, err := s.GetTraffic(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	diffs := map[int64]Traffic{}
	for uid, v := range cur {
		prev := s.last[uid]
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
		s.last[uid] = v
	}
	// 清理已不在线的用户快照
	for uid := range s.last {
		if _, ok := cur[uid]; !ok {
			delete(s.last, uid)
		}
	}
	return diffs, nil
}

// Reset 清空快照（xray 重启后调用，避免把重启前的旧流量当增量）
func (s *StatsCollector) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = map[int64]Traffic{}
}
