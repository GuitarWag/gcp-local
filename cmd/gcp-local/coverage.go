package main

import (
	"os"
	"runtime/coverage"
)

// flushCoverage writes counters before shutdown in a -cover build, so a
// slow shutdown that ends in SIGKILL keeps the data. It is a no-op otherwise.
func flushCoverage() {
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		_ = coverage.WriteCountersDir(dir)
	}
}
