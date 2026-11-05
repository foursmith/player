package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/foursmith/player/internal/projection"
)

type recognitionTrace struct {
	id      uint64
	started time.Time
	logger  *log.Logger
}

func (t *recognitionTrace) printf(format string, args ...any) {
	if t != nil {
		t.logger.Printf("[recognition-test #%d] "+format, append([]any{t.id}, args...)...)
	}
}

func projectionValues[T int | float32 | float64](values []T) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%s=%v", projection.IDs[i], value)
	}
	return strings.Join(parts, " ")
}
