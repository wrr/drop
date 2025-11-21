package log

import (
	"fmt"
	"os"
)

// Info logs an info message to stderr.
func Info(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Drop: "+format+"\n", args...)
}
