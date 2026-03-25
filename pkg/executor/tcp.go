package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/iammm0/execgo/pkg/models"
)

const defaultTCPTimeoutMS = 5000

// TCPMaxDialTimeout 单次拨号超时上限 / max dial timeout per task.
const TCPMaxDialTimeout = 60 * time.Second

// TCPParams TCP 连通性探测参数 / TCP dial probe parameters.
type TCPParams struct {
	Address   string `json:"address"`
	TimeoutMS int64  `json:"timeout_ms,omitempty"`
}

// TCPExecutor 检测 TCP 端口是否可达 / checks TCP connectivity to host:port.
type TCPExecutor struct{}

func (e *TCPExecutor) Type() string { return "tcp" }

func (e *TCPExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
	var p TCPParams
	if err := json.Unmarshal(task.Params, &p); err != nil {
		return nil, fmt.Errorf("parse tcp params: %w", err)
	}
	if p.Address == "" {
		return nil, fmt.Errorf("address is required (host:port)")
	}

	timeout := time.Duration(defaultTCPTimeoutMS) * time.Millisecond
	if p.TimeoutMS > 0 {
		timeout = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	if timeout > TCPMaxDialTimeout {
		return nil, fmt.Errorf("timeout_ms exceeds max of %d ms", TCPMaxDialTimeout.Milliseconds())
	}

	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", p.Address)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}
	_ = c.Close()

	return json.Marshal(map[string]any{
		"ok":      true,
		"address": p.Address,
	})
}
