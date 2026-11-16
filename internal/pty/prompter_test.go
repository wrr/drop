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
	"slices"
	"testing"
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
