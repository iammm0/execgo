package execgocli

import (
	"context"
	"fmt"
)

// CancelResult is the JSON data shape returned by the cancel subcommand.
type CancelResult struct {
	Tasks []map[string]any `json:"tasks"`
}

// Cancel requests cancellation for each task id through ExecGo's cancel endpoint.
func Cancel(ctx context.Context, c *Client, ids []string) (*CancelResult, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("no task ids")
	}
	out := &CancelResult{Tasks: make([]map[string]any, 0, len(ids))}
	for _, id := range ids {
		m, _, _, err := c.PostCancelTask(ctx, id)
		if err != nil {
			return out, err
		}
		out.Tasks = append(out.Tasks, m)
	}
	return out, nil
}
