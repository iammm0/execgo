package unit_test

import (
	"testing"

	"github.com/iammm0/execgo/pkg/fsm"
	"github.com/iammm0/execgo/pkg/models"
)

func TestFSM_TransitionValidation(t *testing.T) {
	if !fsm.CanTransition(models.StatusPending, models.StatusReady) {
		t.Fatal("expected pending -> ready to be allowed")
	}
	if !fsm.CanTransition(models.StatusRunning, models.StatusRetrying) {
		t.Fatal("expected running -> retrying to be allowed")
	}
	if fsm.CanTransition(models.StatusReady, models.StatusSuccess) {
		t.Fatal("expected ready -> success to be rejected")
	}
	if err := fsm.EnsureTransition("t1", models.StatusReady, models.StatusSuccess); err == nil {
		t.Fatal("expected illegal transition error")
	}
}
