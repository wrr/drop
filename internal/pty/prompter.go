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
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	// Columns taken by "│ " and " │".
	frameWidth  = 4
	boxTitle    = " Drop "
	tooSmallMsg = "Drop: terminal too small"
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

// Ask displays a dialog window to the user with the question and
// choices describing the possible answers. Returns the choice selected
// by the user. Fails if the terminal size is not known.
//
// formatQuestion returns the question lines, each at most maxWidth
// long. It is called for each draw, so the lines can be reformatted
// when the terminal is resized. If the lines are longer, a "terminal
// too small" message is shown instead.
func (p *Prompter) Ask(formatQuestion func(maxWidth int) []string, choices []Choice) (Choice, error) {
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
	d, err = showDialog(p.fwd, formatQuestion, choices)
	if err != nil {
		return Choice{}, err
	}
	for {
		key, resized, err := p.fwd.waitEvent()
		if err != nil {
			return Choice{}, err
		}
		if resized {
			if err := d.refresh(); err != nil {
				return Choice{}, err
			}
			continue
		}
		if d.tooSmall {
			// Ignore all keys, the user can't see the choices.
			continue
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
	fwd            *forwarder
	formatQuestion func(maxWidth int) []string
	choices        []Choice

	rows, boxRows int
	// Terminal is too small to show the question.
	tooSmall bool
	// Rows the content was scrolled up to make room for the dialog.
	scrolledRows int
}

func showDialog(fwd *forwarder, formatQuestion func(maxWidth int) []string, choices []Choice) (*dialog, error) {
	rows, cols, err := fwd.winSize()
	if err != nil {
		return nil, err
	}
	if rows == 0 || cols == 0 {
		// Some terminals, for example serial consoles, don't report the
		// size.
		return nil, errors.New("terminal size unknown")
	}
	d := &dialog{
		fwd:            fwd,
		formatQuestion: formatQuestion,
		choices:        choices,
	}
	box := d.layout(rows, cols)
	// If cursorRow fails, the dialog overwrites the bottom rows.
	if cursorRow, err := fwd.cursorRow(); err == nil {
		d.scrolledRows = scrollRows(rows, cursorRow, d.boxRows)
	}
	// The program may have selected the line drawing charset, by
	// switching to G1 or loading it into G0, which would mangle the
	// text, so G0 with ASCII is selected.
	s := saveCursor + selectASCII + g0ASCII + scrollUp(d.scrolledRows) + box
	if err := fwd.write(s); err != nil {
		return nil, err
	}
	return d, nil
}

// scrollRows returns by how many rows to scroll the content up, so
// the dialog with boxRows fits below the cursor row. The rows below
// the cursor are assumed to be empty, which holds for programs that
// print line by line. Full-screen programs are expected to redraw
// after the dialog anyway.
func scrollRows(rows, cursorRow, boxRows int) int {
	free := rows - 1 - cursorRow
	return min(max(boxRows-free, 0), boxRows)
}

// refresh redraws the dialog after a terminal resize.
//
// Terminals handle resizes differently: some keep the existing lines
// in place, some move them down, some add wrapping. To ensure
// consistent resize behavior, the whole screen is cleared, and only
// the dialog is redrawn at the bottom.
func (d *dialog) refresh() error {
	rows, cols, err := d.fwd.winSize()
	if err != nil {
		return err
	}
	// Clear the whole window, ignore error
	d.fwd.write(eraseRows(0, rows))
	d.scrolledRows = 0
	return d.fwd.write(d.layout(rows, cols))
}

// layout returns the sequences that draw the dialog using the given
// terminal size and sets the dialog geometry.
func (d *dialog) layout(rows, cols int) string {
	question := d.formatQuestion(max(cols-frameWidth, 0))
	lines := make([]string, len(question))
	for i, line := range question {
		lines[i] = sanitize(line)
	}
	s, boxRows, fits := questionBox(lines, answerRows(d.choices), rows, cols)
	if !fits {
		s, boxRows = tooSmallBox(rows, cols)
	}
	d.rows = rows
	d.boxRows = boxRows
	d.tooSmall = !fits
	return s
}

// dismiss erases the dialog and restores the cursor. The content
// scrolled up for the dialog stays scrolled up, and the
// cursor is moved up to the end of the scrolled content.
func (d *dialog) dismiss() {
	erase := eraseRows(d.rows-d.boxRows, d.rows)
	// Ignore error
	d.fwd.write(erase + restoreCursor + cursorUp(d.scrolledRows))
}

// eraseRows returns the sequences that erase rows in the [rowStart,
// rowEnd) range.
func eraseRows(rowStart, rowEnd int) string {
	var s strings.Builder
	for row := rowStart; row < rowEnd; row++ {
		s.WriteString(clearLine(row))
	}
	return s.String()
}

// questionBox returns the sequences that draw the question in a box
// over the bottom lines of the terminal, and the number of the box
// rows. Returns false if the box doesn't fit.
func questionBox(lines, answers []string, rows, cols int) (string, int, bool) {
	width := cols
	innerWidth := width - frameWidth
	if len(lines) == 0 {
		lines = []string{""}
	}
	cursorIdx := cursorLine(lines)
	lines = append(lines, answers...)
	questionRows := 2 + len(lines)
	needCols := utf8.RuneCountInString(boxTitle) + 2
	for _, line := range lines {
		needCols = max(needCols, utf8.RuneCountInString(line)+frameWidth)
	}
	if questionRows > rows || needCols > cols {
		return "", 0, false
	}
	top := rows - questionRows

	rule := strings.Repeat("─", width-2)

	var box strings.Builder
	titleRule := strings.Repeat("─", width-2-utf8.RuneCountInString(boxTitle))
	box.WriteString(clearLine(top) + "┌" + boxTitle + titleRule + "┐")
	for i, line := range lines {
		box.WriteString(clearLine(top+1+i) + "│ " + pad(line, innerWidth) + " │")
	}
	box.WriteString(clearLine(rows-1) + "└" + rule + "┘")
	col := frameWidth/2 + utf8.RuneCountInString(lines[cursorIdx])
	box.WriteString(moveTo(top+1+cursorIdx, col))
	return box.String(), questionRows, true
}

// cursorLine returns the index of the line after which the cursor
// should be put: the last line ending with "?", or the last line if
// no line ends with "?". questionLines can't be empty.
func cursorLine(questionLines []string) int {
	for i := len(questionLines) - 1; i >= 0; i-- {
		if strings.HasSuffix(questionLines[i], "?") {
			return i
		}
	}
	return len(questionLines) - 1
}

// tooSmallBox returns the sequences that draw the terminal too small
// message in the last line of the terminal and the number of the box
// rows.
func tooSmallBox(rows, cols int) (string, int) {
	msg := []rune(tooSmallMsg)
	msg = msg[:min(len(msg), cols)]
	return clearLine(rows-1) + string(msg), 1
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
	return resetStyle + moveTo(row, 0) + eraseLine + promptStyle
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
