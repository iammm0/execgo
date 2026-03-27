package executor

import "github.com/iammm0/execgo/pkg/models"

// NormalizeTask 将 legacy type 映射到 V2 语义。
func NormalizeTask(task *models.Task) {
	if task == nil || task.Type == "" {
		return
	}
	switch task.Type {
	case "mcp", "cli-skills", "os":
		return
	default:
		if IsOSTool(task.Type) {
			task.ToolName = task.Type
			task.Category = "os"
			task.Type = "os"
			if len(task.Input) == 0 {
				task.Input = task.Params
			}
		}
	}
}

