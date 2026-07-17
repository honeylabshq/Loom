//go:build !ndpi

package classify

// New returns a no-op classifier when built without the `ndpi` tag. It always
// classifies as "" so the pipeline behaves exactly as it did before nDPI: the
// ClickHouse fallback classifier still runs. This keeps `go test`, CI, and
// dev builds working without libndpi.
func New() (Classifier, error) { return noop{}, nil }

type noop struct{}

func (noop) Classify(_ []byte, _, _ uint16) string { return "" }
