package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/iammm0/execgo/pkg/models"
)

// DNSParams DNS 执行器参数 / DNS executor parameters.
type DNSParams struct {
	Name   string `json:"name"`
	Record string `json:"record,omitempty"` // ip | txt | cname，默认 ip / default ip
}

// DNSExecutor 使用系统解析器做 DNS 查询 / DNS lookups via system resolver.
type DNSExecutor struct{}

func (e *DNSExecutor) Type() string { return "dns" }

func (e *DNSExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
	var p DNSParams
	if err := json.Unmarshal(task.Params, &p); err != nil {
		return nil, fmt.Errorf("parse dns params: %w", err)
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}

	rec := strings.ToLower(strings.TrimSpace(p.Record))
	if rec == "" {
		rec = "ip"
	}

	r := &net.Resolver{PreferGo: true}

	switch rec {
	case "ip":
		addrs, err := r.LookupHost(ctx, p.Name)
		if err != nil {
			return nil, fmt.Errorf("lookup host: %w", err)
		}
		return json.Marshal(map[string]any{"name": p.Name, "record": "ip", "addrs": addrs})
	case "txt":
		txts, err := r.LookupTXT(ctx, p.Name)
		if err != nil {
			return nil, fmt.Errorf("lookup txt: %w", err)
		}
		return json.Marshal(map[string]any{"name": p.Name, "record": "txt", "txt": txts})
	case "cname":
		cname, err := r.LookupCNAME(ctx, p.Name)
		if err != nil {
			return nil, fmt.Errorf("lookup cname: %w", err)
		}
		return json.Marshal(map[string]any{"name": p.Name, "record": "cname", "cname": cname})
	default:
		return nil, fmt.Errorf("record must be one of: ip, txt, cname (got %q)", p.Record)
	}
}
