package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/iammm0/execgo/internal/models"
)

// HTTPParams HTTP 执行器参数 / HTTP executor parameters.
type HTTPParams struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// HTTPExecutor 通过 HTTP 请求执行任务 / executes tasks via HTTP requests.
type HTTPExecutor struct{}

func (e *HTTPExecutor) Type() string { return "http" }

func (e *HTTPExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
	var p HTTPParams
	if err := json.Unmarshal(task.Params, &p); err != nil {
		return nil, fmt.Errorf("parse http params: %w", err)
	}

	if p.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if p.Method == "" {
		p.Method = http.MethodGet
	}

	var bodyReader io.Reader
	if p.Body != "" {
		bodyReader = strings.NewReader(p.Body)
	}

	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 限制 1MB / limit 1MB
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	result := map[string]any{
		"status_code": resp.StatusCode,
		"body":        string(respBody),
	}

	if resp.StatusCode >= 400 {
		return json.Marshal(result) // 仍然返回结果但标记错误 / still return result but mark error
	}

	return json.Marshal(result)
}
