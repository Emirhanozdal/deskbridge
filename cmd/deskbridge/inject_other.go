//go:build !darwin && !linux

package main

import (
	"fmt"
	"runtime"
)

// newInjector reports that input injection is unsupported on this OS. The control
// channel still drains events (serveControlSink ignores them) so the video path
// keeps working.
func newInjector() (controlInjector, error) {
	return nil, fmt.Errorf("input injection is not supported on %s", runtime.GOOS)
}
