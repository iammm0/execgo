package worker

import (
	"strings"

	"github.com/iammm0/execgo/pkg/executor"
)

func defaultCapabilities(runnerName string, overrides map[string]string) map[string]string {
	caps := map[string]string{
		"executor": strings.Join(executor.AcceptedTaskTypes(), ","),
		"sandbox":  runnerName,
	}
	for k, v := range overrides {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		caps[key] = strings.TrimSpace(v)
	}
	return caps
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
