// Copyright 2026 Jan Wrobel <jan@mixedbit.org>
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
	"strings"
	"testing"
)

func TestDebugLevel(t *testing.T) {
	var b strings.Builder
	New(&b).Debug("message")
	if b.Len() > 0 {
		t.Errorf("debug logged")
	}
	t.Setenv(debugEnvVar, "1")
	New(&b).Debug("message")
	got := b.String()
	if !strings.Contains(got, "message") {
		t.Errorf("debug not logged %s", got)
	}
}
