package engine

import (
	"context"
	"errors"
)

// AIResult is the owned outcome of one AIPredictionEngine call. Not every field
// is meaningful for every method: Pass and Reasoning describe an assertion,
// Defects lists findDefects results, and Text carries extracted text.
type AIResult struct {
	Pass      bool
	Reasoning string
	Text      string
	Defects   []string
}

// AIPredictionEngine is the injectable, screenshot-based AI boundary declared in
// specs/01-core-engine.md. Every call receives an uncompressed PNG screenshot of
// the current screen. A nil engine on Dependencies fails closed with
// ErrCloudAPIKeyNotAvailable when the provider key is unavailable.
type AIPredictionEngine interface {
	FindDefects(ctx context.Context, screenshotPNG []byte) (AIResult, error)
	PerformAssertion(ctx context.Context, screenshotPNG []byte, assertion string) (AIResult, error)
	ExtractText(ctx context.Context, screenshotPNG []byte, query string) (AIResult, error)
}

// ErrCloudAPIKeyNotAvailable marks an AI command reached execution without a
// configured AIPredictionEngine (missing engine or API key). It is wrapped in a
// OperationError so the failure stays retryable and, under the AI commands'
// default optional=true, is warned rather than fatal.
var ErrCloudAPIKeyNotAvailable = errors.New("cloud API key is not available for AI execution")

func newCloudAPIKeyNotAvailableError(keyword string) *OperationError {
	return NewOperationError(keyword+" requires a configured AI provider", ErrCloudAPIKeyNotAvailable)
}
