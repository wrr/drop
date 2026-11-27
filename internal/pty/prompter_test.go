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
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSanitize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"allow example.com?", "allow example.com?"},
		{"bücher.de", "bücher.de"},
		{"a\x1b[31mb", "a[31mb"},
		{"a\nb\rc\td\x00e", "abcde"},
		{"a\x7fb", "ab"},
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPad(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  string
	}{
		{"", 0, ""},
		{"", 3, "   "},
		{"ab", 5, "ab   "},
		{"abc", 3, "abc"},
		{"abcd", 3, "abcd"},
		{"bü", 4, "bü  "},
	}
	for _, c := range cases {
		if got := pad(c.text, c.width); got != c.want {
			t.Errorf("pad(%q, %d) = %q, want %q", c.text, c.width, got, c.want)
		}
	}
}

func TestCursorLine(t *testing.T) {
	cases := []struct {
		questionLines []string
		want          int
	}{
		{[]string{""}, 0},
		{[]string{"first", "second?", "thi?rd"}, 1},
		{[]string{"first?", "second?", "thi?rd"}, 1},
		{[]string{"first", "second", "thi?rd"}, 2},
	}
	for _, c := range cases {
		if got := cursorLine(c.questionLines); got != c.want {
			t.Errorf("cursorLine(%q) = %d, want %d", c.questionLines, got, c.want)
		}
	}
}

func TestScrollRows(t *testing.T) {
	cases := []struct {
		cursorRow, want int
	}{
		{0, 0},
		{5, 0},
		{6, 1},
		{9, 4},
		// Rows are 0-based, a reply out of range scrolls at most the
		// box height.
		{12, 4},
	}
	for _, c := range cases {
		if got := scrollRows(10, c.cursorRow, 4); got != c.want {
			t.Errorf("cursor row %d: scroll %d, want %d", c.cursorRow, got, c.want)
		}
	}
}

func TestAnswerRows(t *testing.T) {
	choices := []Choice{
		{Key: 'a', Label: "[a]llow"},
		{Key: 'd', Label: "[d]eny"},
		{Key: 'A', Label: "[A]llow always"},
		{Key: 'D', Label: "[D]eny always"},
		{Key: 'x', Label: "[x]"},
	}
	want := []string{
		"[a]llow          [d]eny",
		"[A]llow always   [D]eny always",
		"[x]",
	}
	if got := answerRows(choices); !slices.Equal(got, want) {
		t.Errorf("answerRows() = %q, want %q", got, want)
	}
}

func TestQuestionBoxFits(t *testing.T) {
	lines := []string{"Allow example.com?"}
	answers := []string{"[a]llow"}
	// 18 columns of text plus the frame, 2 lines plus the top and bottom
	// border.
	cases := []struct {
		rows, cols int
		want       bool
	}{
		{4, 22, true},
		{10, 80, true},
		{3, 22, false},
		{4, 21, false},
	}
	for _, c := range cases {
		_, boxRows, fits := questionBox(lines, answers, c.rows, c.cols)
		if fits != c.want {
			t.Errorf("%dx%d: fits = %v, want %v", c.cols, c.rows, fits, c.want)
		}
		if fits && boxRows != 4 {
			t.Errorf("%dx%d: box rows = %d, want 4", c.cols, c.rows, boxRows)
		}
	}
}

func TestTooSmallBox(t *testing.T) {
	cases := []struct {
		cols int
		want string
	}{
		{80, tooSmallMsg},
		{len(tooSmallMsg), tooSmallMsg},
		{4, "Drop"},
		{1, "D"},
	}
	for _, c := range cases {
		s, rows := tooSmallBox(5, c.cols)
		if want := clearLine(4) + c.want; s != want {
			t.Errorf("cols %d: got %q, want %q", c.cols, s, want)
		}
		if rows != 1 {
			t.Errorf("cols %d: rows = %d, want 1", c.cols, rows)
		}
	}
}

// readUntil reads from the file until the read data contains s.
func readUntil(t *testing.T, file *os.File, s string) {
	t.Helper()
	file.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got []byte
	buf := make([]byte, 1024)
	for !strings.Contains(string(got), s) {
		n, err := file.Read(buf)
		if err != nil {
			t.Fatalf("read %q waiting for %q: %v", got, s, err)
		}
		got = append(got, buf[:n]...)
	}
}

func TestAskRedrawsOnResize(t *testing.T) {
	tf := newTestForwarder(t, true)
	if err := setWinsize(tf.hostOutputReader, &unix.Winsize{Row: 10, Col: 40}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		choice Choice
		err    error
	}
	done := make(chan result)
	go func() {
		formatQuestion := func(maxWidth int) []string {
			return []string{fmt.Sprintf("width %d", maxWidth)}
		}
		choice, err := newPrompter(tf.fwd).Ask(formatQuestion, []Choice{{Key: 'y', Label: "[y]es"}})
		done <- result{choice, err}
	}()
	readUntil(t, tf.hostOutputReader, cursorPositionQuery)
	write(t, tf.hostInputWriter, "\x1b[1;1R")
	readUntil(t, tf.hostOutputReader, "width 36")

	if err := setWinsize(tf.hostOutputReader, &unix.Winsize{Row: 10, Col: 60}); err != nil {
		t.Fatal(err)
	}
	tf.resized <- syscall.SIGWINCH
	readUntil(t, tf.hostOutputReader, "width 56")

	noInputDiscard(tf.fwd)
	write(t, tf.hostInputWriter, "y")
	select {
	case r := <-done:
		if r.err != nil || r.choice.Key != 'y' {
			t.Errorf("Ask = %q, %v, want y", r.choice.Key, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask didn't return")
	}
}

func TestAskScrollsContentForDialog(t *testing.T) {
	tf := newTestForwarder(t, true)
	if err := setWinsize(tf.hostOutputReader, &unix.Winsize{Row: 10, Col: 40}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		formatQuestion := func(int) []string { return []string{"Allow?"} }
		_, err := newPrompter(tf.fwd).Ask(formatQuestion, []Choice{{Key: 'y', Label: "[y]es"}})
		done <- err
	}()
	readUntil(t, tf.hostOutputReader, cursorPositionQuery)
	// The cursor in the last row, the 4 rows box needs all of them.
	write(t, tf.hostInputWriter, "\x1b[10;1R")
	readUntil(t, tf.hostOutputReader, saveCursor+selectASCII+g0ASCII+scrollUp(4))

	noInputDiscard(tf.fwd)
	write(t, tf.hostInputWriter, "y")
	readUntil(t, tf.hostOutputReader, restoreCursor+cursorUp(4))
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask didn't return")
	}
}
