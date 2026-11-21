//go:build !windows

package watcher

type StubProcessEnumerator struct{}

func NewProcessEnumerator() ProcessEnumerator {
	return &StubProcessEnumerator{}
}

func (e *StubProcessEnumerator) EnumerateProcesses() ([]ProcessInfo, uint32, error) {
	return nil, 0, nil
}
