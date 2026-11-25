package ai

import "textus/backend/ai/rules"

// The character classes the completion's cleaning shares with the rules
// engine, under the names Rust's char methods have.

func isAlphabetic(r rune) bool   { return rules.IsAlphabetic(r) }
func isAlphanumeric(r rune) bool { return rules.IsAlphanumeric(r) }
func lines(text string) []string { return rules.Lines(text) }
