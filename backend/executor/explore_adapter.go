package executor

import (
	"context"

	"github.com/browserwing/browserwing/models"
)

// ExploreAdapter adapts the Executor to the ExecutorRecorderInterface
// used by the browser.Explorer to avoid import cycles.
type ExploreAdapter struct {
	executor *Executor
}

// NewExploreAdapter creates a new adapter
func NewExploreAdapter(exec *Executor) *ExploreAdapter {
	return &ExploreAdapter{executor: exec}
}

func (a *ExploreAdapter) StartRecordMode() {
	a.executor.StartRecordMode()
}

func (a *ExploreAdapter) StopRecordMode() []models.OpRecord {
	return a.executor.StopRecordMode()
}

func (a *ExploreAdapter) GetRecordedOps() []models.OpRecord {
	return a.executor.GetRecordedOps()
}

func (a *ExploreAdapter) NavigateForExplore(ctx context.Context, url string) error {
	_, err := a.executor.Navigate(ctx, url, nil)
	return err
}
