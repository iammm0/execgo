package fsm

import (
	"fmt"

	"github.com/iammm0/execgo/pkg/models"
)

// TransitionTable constrains legal status transitions.
var TransitionTable = map[models.TaskStatus]map[models.TaskStatus]struct{}{
	models.StatusPending: {
		models.StatusReady:     {},
		models.StatusRunning:   {},
		models.StatusSuccess:   {},
		models.StatusFailed:    {},
		models.StatusCancelled: {},
		models.StatusSkipped:   {},
	},
	models.StatusReady: {
		models.StatusLeased:    {},
		models.StatusRunning:   {},
		models.StatusCancelled: {},
		models.StatusSkipped:   {},
	},
	models.StatusLeased: {
		models.StatusRunning:   {},
		models.StatusReady:     {}, // lease expired / reclaimed
		models.StatusCancelled: {},
		models.StatusSkipped:   {},
	},
	models.StatusRunning: {
		models.StatusSuccess:      {},
		models.StatusFailed:       {},
		models.StatusTimedOut:     {},
		models.StatusCancelled:    {},
		models.StatusRetrying:     {},
		models.StatusCompensating: {},
	},
	models.StatusFailed: {
		models.StatusRetrying:     {},
		models.StatusCompensating: {},
	},
	models.StatusTimedOut: {
		models.StatusRetrying: {},
	},
	models.StatusRetrying: {
		models.StatusReady:   {},
		models.StatusSkipped: {},
	},
	models.StatusCompensating: {
		models.StatusCompensated: {},
		models.StatusFailed:      {},
	},
}

// CanTransition returns whether transition is allowed.
func CanTransition(from, to models.TaskStatus) bool {
	if from == "" {
		return to == models.StatusPending || to == models.StatusReady
	}
	if from == to {
		return true
	}
	nextSet, ok := TransitionTable[from]
	if !ok {
		return false
	}
	_, ok = nextSet[to]
	return ok
}

// EnsureTransition validates task status transition.
func EnsureTransition(taskID string, from, to models.TaskStatus) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("task %s: illegal status transition %s -> %s", taskID, from, to)
}
