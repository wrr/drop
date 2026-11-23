// Copyright 2025 Jan Wrobel <jan@mixedbit.org>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wrr/drop/internal/osutil"
)

const debugEnvVar string = "DROP_DEBUG"

// Stderr prints a message for the user to stderr. It should be used
// only in the sandbox setup phase, before the sandboxed program
// starts and writes own output to stdout/err.
func Stderr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Drop: "+format+"\n", args...)
}

func level() slog.Level {
	if os.Getenv(debugEnvVar) != "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// OpenFile opens the log file at path for appending, creating it
// and its parent directories if needed.
func OpenFile(path string) (*os.File, error) {
	if err := osutil.MkdirAll(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %v", err)
	}
	return f, nil
}

func New(writer io.Writer) *slog.Logger {
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level()})
	return slog.New(handler)
}
