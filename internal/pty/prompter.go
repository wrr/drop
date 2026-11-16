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

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// Columns taken by "│ " and " │".
	frameWidth = 4
	boxTitle   = " Drop "
	// Spaces between the answer columns.
	answerGap = 3
)

// Choice is one of the answers to a question.
type Choice struct {
	// Key the user presses, unique within the question.
	Key byte
	// Shown in the question, for example "[a]llow".
	Label string
	// Echoed back after the question is answered.
	Answer string
}

// Prompter shows questions to the user on the terminal.
type Prompter struct {
	fwd *forwarder
}

func newPrompter(fwd *forwarder) *Prompter {
	return &Prompter{
		fwd: fwd,
	}
}

// MaxWidth returns how long the question lines can be, 0 if the
// terminal size is not known. It's a hint for formatting the question:
// Ask returns an error instead of showing a question with longer lines.
func (p *Prompter) MaxWidth() int {
	_, cols, err := p.fwd.winSize()
	if err != nil {
		return 0
	}
	return max(cols-frameWidth, 0)
}

// Ask displays a dialog window to the user with the question lines,
// each at most MaxWidth long, and choices describing the possible
// answers. Returns the choice selected by the user.
func (p *Prompter) Ask(question []string, choices []Choice) (Choice, error) {
	if err := p.fwd.pause(); err != nil {
		return Choice{}, err
	}
	var d *dialog
	defer func() {
		if d != nil {
			d.dismiss()
		}
		p.fwd.resume()
		if d != nil {
			p.fwd.repaint()
		}
	}()
	var err error
	d, err = showDialog(p.fwd, question, choices)
	if err != nil {
		return Choice{}, err
	}
	for {
		key, err := p.fwd.readKey()
		if err != nil {
			return Choice{}, err
		}
		if choice, ok := choiceWithKey(key, choices); ok {
			return choice, nil
		}
	}
}

func choiceWithKey(key byte, choices []Choice) (Choice, bool) {
	for _, c := range choices {
		if c.Key == key {
			return c, true
		}
	}
	return Choice{}, false
}

// dialog is one question shown on the terminal, with the geometry
// needed to redraw or erase it.
type dialog struct {
	fwd      *forwarder
	question []string
	choices  []Choice

	rows, boxRows int
}

func showDialog(fwd *forwarder, question []string, choices []Choice) (*dialog, error) {
	d := &dialog{
		fwd:      fwd,
		question: question,
		choices:  choices,
	}
	if err := d.show(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *dialog) show() error {
	rows, cols, err := d.fwd.winSize()
	if err != nil {
		return err
	}
	lines := make([]string, len(d.question))
	for i, line := range d.question {
		lines[i] = sanitize(line)
	}
	s, boxRows, err := questionBox(lines, answerRows(d.choices), rows, cols)
	if err != nil {
		return err
	}
	if err := d.fwd.write(s); err != nil {
		return err
	}
	d.rows = rows
	d.boxRows = boxRows
	return nil
}

// dismiss erases the dialog and restores the cursor.
func (d *dialog) dismiss() {
	var restore strings.Builder
	for row := d.rows - d.boxRows + 1; row <= d.rows; row++ {
		restore.WriteString(clearLine(row))
	}
	restore.WriteString(restoreCursor)
	// Ignore error
	d.fwd.write(restore.String())
}

// questionBox returns the sequences that draw the question in a box
// over the bottom lines of the terminal.
func questionBox(lines, answers []string, rows, cols int) (string, int, error) {
	width := cols
	innerWidth := width - frameWidth
	lines = append(lines, answers...)
	questionRows := 2 + len(lines)
	needCols := utf8.RuneCountInString(boxTitle) + 2
	for _, line := range lines {
		needCols = max(needCols, utf8.RuneCountInString(line)+frameWidth)
	}
	if questionRows > rows || needCols > cols {
		return "", 0, fmt.Errorf("terminal too small to display dialog: needs %dx%d, has %dx%d",
			needCols, questionRows, cols, rows)
	}
	top := rows - questionRows + 1

	rule := strings.Repeat("─", width-2)

	var box strings.Builder
	// The program may have selected the line drawing charset, by
	// switching to G1 or loading it into G0, which would mangle the
	// text, so G0 with ASCII is selected.
	box.WriteString(saveCursor + selectASCII + g0ASCII)
	titleRule := strings.Repeat("─", width-2-utf8.RuneCountInString(boxTitle))
	box.WriteString(clearLine(top) + "┌" + boxTitle + titleRule + "┐")
	for i, line := range lines {
		box.WriteString(clearLine(top+1+i) + "│ " + pad(line, innerWidth) + " │")
	}
	box.WriteString(clearLine(rows) + "└" + rule + "┘")
	lastLine := ""
	if len(lines) > 0 {
		lastLine = lines[len(lines)-1]
	}
	box.WriteString(moveTo(rows-1, (frameWidth/2+1)+utf8.RuneCountInString(lastLine)))
	return box.String(), questionRows, nil
}

// answerRows lays out the choice labels two per row, aligned in
// columns.
func answerRows(choices []Choice) []string {
	colWidth := 0
	for i := 0; i < len(choices); i += 2 {
		colWidth = max(colWidth, utf8.RuneCountInString(choices[i].Label))
	}
	var rows []string
	for i := 0; i < len(choices); i += 2 {
		row := choices[i].Label
		if i+1 < len(choices) {
			row = pad(row, colWidth+answerGap) + choices[i+1].Label
		}
		rows = append(rows, row)
	}
	return rows
}

// pad fills the text with spaces up to the given number of columns.
func pad(text string, width int) string {
	missing := width - utf8.RuneCountInString(text)
	if missing <= 0 {
		return text
	}
	return text + strings.Repeat(" ", missing)
}

// clearLine returns the sequences that erase the line and leave the
// cursor in its first column, ready for drawing in the prompt style.
func clearLine(row int) string {
	// Colors are reset first, because the terminal erases with the
	// current background color, which belongs to the sandboxed program.
	return resetStyle + moveTo(row, 1) + eraseLine + promptStyle
}

// sanitize removes terminal control characters from s.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
