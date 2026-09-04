package main

// arrange, scatter and focus rearrange live peers across tmux panes, which windows has no
// backend for in phase 1. These shims let the untagged dispatch resolve on windows and
// return the phase-1 refusal; the text comes from phase1Refusal so it stays identical to
// the template TestWindowsRefusalTemplateCoversEveryExcludedVerb checks.
func runArrange([]string) int { return refuseLayout("arrange") }
func runScatter([]string) int { return refuseLayout("scatter") }
func runFocus([]string) int   { return refuseLayout("focus") }

// refuseLayout guards against a missing phase1Refusal key: without this, dropping a key
// would make the verb die with an EMPTY message and rc 1 — the unsupported-vs-mistyped
// confusion the refusal exists to prevent. The refusedVerbs rows + template-coverage test
// also catch it, but the guard makes it structural rather than test-dependent.
func refuseLayout(verb string) int {
	m := phase1Refusal(verb)
	if m == "" {
		m = verb + " is not available on windows in phase 1 (tmux is unix-only)"
	}
	return die("%s", m)
}
