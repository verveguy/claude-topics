package main

import "testing"

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":              "''",
		"plain":         "plain",
		"a=b":           "a=b",           // "=" is fine inside a word
		"=Dispatcher":   "'=Dispatcher'", // zsh would look this up as a command
		"=orac":         "'=orac'",
		"two words":     "'two words'",
		"it's":          `'it'"'"'s'`,
		"/path/to-x_1.": "/path/to-x_1.",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
