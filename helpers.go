package main

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// userOf returns the signed-in user, or a zero User when signed out.
func userOf(ctx context.Context) User {
	u, _ := CurrentUser(ctx)
	return u
}

// agentOf returns the signed-in user's agent, or a zero Agent.
func agentOf(ctx context.Context) Agent { return CurrentAgent(ctx) }

func initial(name string) string {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(name))
	if r == utf8.RuneError {
		return "?"
	}
	return string(r)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %s", n, pluralWord(word))
}

// pluralWord is "tickets", "entries".
func pluralWord(word string) string {
	if strings.HasSuffix(word, "y") && !strings.HasSuffix(word, "ay") && !strings.HasSuffix(word, "ey") {
		return word[:len(word)-1] + "ies"
	}
	return word + "s"
}
