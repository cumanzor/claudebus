package main

import "testing"

func TestResolveRepoSlug(t *testing.T) {
	// $CBUS_REPO overrides the baked default.
	t.Run("env overrides baked", func(t *testing.T) {
		defer func(prev string) { repoSlug = prev }(repoSlug)
		repoSlug = "baked/default"
		t.Setenv("CBUS_REPO", "env/override")
		if got, ok := resolveRepoSlug(); !ok || got != "env/override" {
			t.Errorf("got (%q,%v), want env/override", got, ok)
		}
	})
	// baked default used when the env is unset.
	t.Run("baked default", func(t *testing.T) {
		defer func(prev string) { repoSlug = prev }(repoSlug)
		repoSlug = "baked/default"
		t.Setenv("CBUS_REPO", "")
		if got, ok := resolveRepoSlug(); !ok || got != "baked/default" {
			t.Errorf("got (%q,%v), want baked/default", got, ok)
		}
	})
	// whitespace-only env is treated as unset, so it falls through to the baked slug.
	t.Run("blank env falls through", func(t *testing.T) {
		defer func(prev string) { repoSlug = prev }(repoSlug)
		repoSlug = "baked/default"
		t.Setenv("CBUS_REPO", "   ")
		if got, ok := resolveRepoSlug(); !ok || got != "baked/default" {
			t.Errorf("got (%q,%v), want baked/default", got, ok)
		}
	})
	// neither set: not configured, caller prints the remedy.
	t.Run("empty everywhere", func(t *testing.T) {
		defer func(prev string) { repoSlug = prev }(repoSlug)
		repoSlug = ""
		t.Setenv("CBUS_REPO", "")
		if got, ok := resolveRepoSlug(); ok || got != "" {
			t.Errorf("got (%q,%v), want unconfigured", got, ok)
		}
	})
}
