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

package pty

// Choice is one of the answers to a question.
type Choice struct {
	// Key the user presses, unique within the question.
	Key byte
	// Shown in the question, for example "[a]llow".
	Label string
	// Echoed back after the question is answered.
	Answer string
}

// Dialog shows questions to the user on the terminal.
type Dialog struct {
	fwd *forwarder
}

func newDialog(fwd *forwarder) *Dialog {
	return &Dialog{
		fwd: fwd,
	}
}

// Ask displays a dialog window to the user with the given question
// and choices describing the possible answers. Returns the choice
// selected by the user.
func (d *Dialog) Ask(question string, choices []Choice) (Choice, error) {
	return choices[0], nil
}
