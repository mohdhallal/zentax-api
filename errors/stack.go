package apperrors

import (
	"fmt"
	"runtime"
	"strings"
)

type StackTracer interface {
	StackTrace() string
}

func captureStack() string {
	const maxDepth = 32
	pcs := make([]uintptr, maxDepth) //nolint:makezero // runtime.Callers requires pre-allocated buffer
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	var sb strings.Builder
	for {
		frame, more := frames.Next()
		fmt.Fprintf(&sb, "%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line)
		if !more {
			break
		}
	}
	return sb.String()
}
