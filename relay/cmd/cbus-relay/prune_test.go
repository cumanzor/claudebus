package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"claudebus/internal/core"
	"claudebus/relay/internal/spool"
)

func seedIdle(t *testing.T, st spool.Store, ch, al string) {
	t.Helper()
	if _, err := st.Write(ch, al, []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	names, _ := st.ListNew(ch, al)
	if err := st.MarkDelivered(ch, al, names[0]); err != nil {
		t.Fatal(err)
	}
}

func prune(t *testing.T, s *server, query, auth string) (*httptest.ResponseRecorder, core.PruneResponse) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/prune"+query, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	s.handlePrune(w, r)
	var out core.PruneResponse
	if w.Code == http.StatusOK {
		_ = json.NewDecoder(w.Body).Decode(&out)
	}
	return w, out
}

func TestHandlePrune(t *testing.T) {
	st := spool.Store{Root: t.TempDir()}
	s := &server{store: st, hub: newHub(), token: "tok"}

	seedIdle(t, st, "c", "idle")               // off, no mail -> prunable
	st.Write("c", "pending", []byte("wait\n")) // off, queued -> keep
	seedIdle(t, st, "c", "live")               // no mail BUT connected -> keep
	s.hub.attach("c/live")
	seedIdle(t, st, "other", "idle") // different channel

	// channel-scoped: only c/idle goes.
	w, out := prune(t, s, "?channel=c", "Bearer tok")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if len(out.Pruned) != 1 || out.Pruned[0] != "c/idle" {
		t.Fatalf("pruned = %v, want [c/idle]", out.Pruned)
	}
	if peers, _ := st.Peers(); peers["c/pending"] != 1 {
		t.Fatalf("pending mail lost: %v", peers)
	}

	// unscoped sweep now takes other/idle (c/idle already gone, c/live connected).
	_, out = prune(t, s, "", "Bearer tok")
	if len(out.Pruned) != 1 || out.Pruned[0] != "other/idle" {
		t.Fatalf("pruned = %v, want [other/idle]", out.Pruned)
	}
}

func TestHandlePruneAuthAndMethod(t *testing.T) {
	s := &server{store: spool.Store{Root: t.TempDir()}, hub: newHub(), token: "tok"}

	if w, _ := prune(t, s, "", "Bearer nope"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", w.Code)
	}
	if w, _ := prune(t, s, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no auth status = %d, want 401", w.Code)
	}

	r := httptest.NewRequest(http.MethodGet, "/prune", nil)
	r.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.handlePrune(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", w.Code)
	}

	r = httptest.NewRequest(http.MethodPost, "/prune?channel=bad/name", nil)
	r.Header.Set("Authorization", "Bearer tok")
	w = httptest.NewRecorder()
	s.handlePrune(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad channel status = %d, want 400", w.Code)
	}
}
