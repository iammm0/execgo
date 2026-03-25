package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/iammm0/execgo/internal/models"
)

// FileParams 文件执行器参数 / File executor parameters.
type FileParams struct {
	Action  string `json:"action"`  // read, write, append, delete, stat
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

// FileExecutor 执行文件系统操作 / executes file system operations.
type FileExecutor struct{}

func (e *FileExecutor) Type() string { return "file" }

func (e *FileExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
	var p FileParams
	if err := json.Unmarshal(task.Params, &p); err != nil {
		return nil, fmt.Errorf("parse file params: %w", err)
	}

	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// 清理路径防止目录穿越 / sanitize path to prevent traversal
	p.Path = filepath.Clean(p.Path)

	switch p.Action {
	case "read":
		return e.read(p.Path)
	case "write":
		return e.write(p.Path, p.Content, false)
	case "append":
		return e.write(p.Path, p.Content, true)
	case "delete":
		return e.delete(p.Path)
	case "stat":
		return e.stat(p.Path)
	default:
		return nil, fmt.Errorf("unknown action %q (supported: read, write, append, delete, stat)", p.Action)
	}
}

func (e *FileExecutor) read(path string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return json.Marshal(map[string]any{
		"content": string(data),
		"size":    len(data),
	})
}

func (e *FileExecutor) write(path, content string, appendMode bool) (json.RawMessage, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	flag := os.O_WRONLY | os.O_CREATE
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	n, err := f.WriteString(content)
	if err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	return json.Marshal(map[string]any{
		"bytes_written": n,
	})
}

func (e *FileExecutor) delete(path string) (json.RawMessage, error) {
	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("delete file: %w", err)
	}
	return json.Marshal(map[string]any{"deleted": true})
}

func (e *FileExecutor) stat(path string) (json.RawMessage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	return json.Marshal(map[string]any{
		"name":     info.Name(),
		"size":     info.Size(),
		"mode":     info.Mode().String(),
		"mod_time": info.ModTime(),
		"is_dir":   info.IsDir(),
	})
}
