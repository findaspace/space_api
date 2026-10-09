package jobs

import (
	"github.com/riverqueue/river"
	"testing"
)

func TestScheduledCleanupKindsHaveWorkers(t *testing.T) {
	workers, err := Register(Deps{})
	if err != nil {
		t.Fatal(err)
	}
	// River refuses a second registration for an existing kind. These checks
	// catch a periodic schedule whose worker was accidentally left unregistered.
	if err = river.AddWorkerSafely(workers, &SweepVideosWorker{}); err == nil {
		t.Fatal("scheduled video sweeper was not registered")
	}
	if err = river.AddWorkerSafely(workers, &SweepDeletedStorageWorker{}); err == nil {
		t.Fatal("scheduled deletion sweeper was not registered")
	}
}
