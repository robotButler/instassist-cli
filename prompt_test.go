package main

import (
	"strings"
	"testing"
)

func TestBuildPromptIncludesUserTextAndSchema(t *testing.T) {
	user := "list files"
	prompt := buildPrompt(user)
	if !strings.Contains(prompt, user) {
		t.Fatalf("expected prompt to contain user text %q", user)
	}
	if !strings.Contains(prompt, `"options":[{"value":"...","description":"...","recommendation_order":1}]`) {
		t.Fatalf("expected prompt to include schema hint, got: %s", prompt)
	}
}

func TestParseOptionsPrefersLastValidBlock(t *testing.T) {
	raw := `noise {"options":[{"value":"one","description":"first","recommendation_order":1}]} trailing {"options":[{"value":"two","description":"second","recommendation_order":2}]}`
	opts, err := parseOptions(raw)
	if err != nil {
		t.Fatalf("parseOptions returned error: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
	if opts[0].Value != "two" {
		t.Fatalf("expected last block to be used, got %q", opts[0].Value)
	}
}

func TestParseOptionsSortsByRecommendationOrder(t *testing.T) {
	raw := `{"options":[{"value":"late","description":"d","recommendation_order":2},{"value":"early","description":"d","recommendation_order":1},{"value":"unsorted","description":"d","recommendation_order":0}]}`
	opts, err := parseOptions(raw)
	if err != nil {
		t.Fatalf("parseOptions returned error: %v", err)
	}
	if len(opts) != 3 {
		t.Fatalf("expected 3 options, got %d", len(opts))
	}
	if opts[0].Value != "early" || opts[1].Value != "late" || opts[2].Value != "unsorted" {
		t.Fatalf("unexpected sort order: %+v", opts)
	}
}

func TestCleanTextCollapsesWhitespace(t *testing.T) {
	in := "  hello \n world\t\t"
	got := cleanText(in)
	want := "hello world"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
