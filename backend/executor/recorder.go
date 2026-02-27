package executor

import (
	"sync"
	"time"

	"github.com/browserwing/browserwing/models"
)

// OperationRecord is a local alias used within the executor package for convenience.
// It mirrors models.OpRecord.
type OperationRecord = models.OpRecord

// OperationRecorder provides recording capabilities that can be attached to an Executor
type OperationRecorder struct {
	mu         sync.Mutex
	enabled    bool
	operations []OperationRecord
}

// NewOperationRecorder creates a new recorder
func NewOperationRecorder() *OperationRecorder {
	return &OperationRecorder{
		operations: make([]OperationRecord, 0),
	}
}

// Enable starts recording
func (r *OperationRecorder) Enable() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = true
	r.operations = make([]OperationRecord, 0)
}

// Disable stops recording
func (r *OperationRecorder) Disable() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = false
}

// IsEnabled checks if recording is active
func (r *OperationRecorder) IsEnabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

// Record adds an operation record
func (r *OperationRecorder) Record(op OperationRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return
	}
	op.Timestamp = time.Now()
	r.operations = append(r.operations, op)
}

// GetOperations returns a copy of all recorded operations
func (r *OperationRecorder) GetOperations() []OperationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	ops := make([]OperationRecord, len(r.operations))
	copy(ops, r.operations)
	return ops
}

// Reset clears all recorded operations
func (r *OperationRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operations = make([]OperationRecord, 0)
}

// Count returns the number of recorded operations
func (r *OperationRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.operations)
}
