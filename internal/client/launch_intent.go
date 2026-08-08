package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// launchIntentTTL is how long an unanswered intent keeps refusing. The costs are
// asymmetric, which is what picks the top of the 2-3 minute range: a spurious refusal
// names its own age and when it expires, and the operator waits; a double-launch onto
// one transcript announces nothing and is discovered later, by its damage.
const launchIntentTTL = 180 * time.Second

// LaunchIntent is a launcher's declaration that it is ABOUT to put a process on a
// session — written before the fork, answered by that session's own join.
//
// It exists for one window: between a fork and the child's re-join, the child holds
// no meta and arms no listener, so liveSids sees nothing and every liveness gate in
// the tree reads the transcript as free. A second launcher arriving in that gap would
// attach a second process to one conversation. The marker is the only evidence the
// first launch happened at all.
//
// It is a marker in the reservation family, NOT a lock: nothing waits on it, nothing
// owns it, and it cannot deadlock — it expires. Exactly two things clear it, the
// same-session join it was written for, and the TTL. Pid and ProcStart are forensic
// provenance for the refusal message (which process promised this launch, and enough
// of an identity to tell a recycled pid from the real one afterwards); they are never
// consulted for a clearing decision. A launcher-liveness rule would be vacuous here,
// because the verb's own process exits on the SUCCESS path too — milliseconds after
// the write, "the launcher is gone" is true of every intent ever written.
type LaunchIntent struct {
	Channel   string `json:"channel"`
	Alias     string `json:"alias"`
	SessionID string `json:"sessionId"`
	Pid       int    `json:"pid"`
	ProcStart string `json:"procStart"`
	TS        string `json:"ts"`
}

// launchIntentPath is the marker's home: a dot-file in the CHANNEL dir, not in the
// peer's own.
//
// The peer dir cannot hold it. An explicit-alias Join does os.RemoveAll(peerDir)
// before recreating it, so ANY session taking that name would destroy the marker —
// a third clearing path, owned by whoever reclaims the alias, which is exactly the
// party the guard is protecting the transcript from. At the channel level the file
// is invisible to every walker (all of them skip non-dirs and dot-prefixed names,
// the same convention .reap. and .formations rely on), so it creates no phantom
// peer, and it survives the normal case where the anchor's peer dir was pruned or
// never existed after a reboot.
func launchIntentPath(ch, alias string) string {
	return filepath.Join(CBUSDir(), ch, ".launch-intent-"+alias+".json")
}

// WriteLaunchIntent records that this process is about to launch sid as ch/alias.
// The channel dir is created if absent: resuming into a channel that no longer exists
// is legal (a reboot takes the whole store's live state with it), and an empty channel
// dir is a harmless artifact.
//
// Callers are channel/alias-general on purpose — the same window exists for any
// launcher that forks a session and waits for it to join (cbus-yca), and adopting
// this should be a call, not surgery.
func WriteLaunchIntent(ch, alias, sid string) error {
	if err := checkStoreName("channel", ch); err != nil {
		return err
	}
	if err := checkStoreName("alias", alias); err != nil {
		return err
	}
	dir := filepath.Join(CBUSDir(), ch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	pid := os.Getpid()
	start, _ := procStartTime(pid) // provenance only: a probe that cannot answer must not stop the guard
	b, err := json.MarshalIndent(LaunchIntent{
		Channel: ch, Alias: alias, SessionID: sid,
		Pid: pid, ProcStart: start, TS: Now(),
	}, "", "  ")
	if err != nil {
		return err
	}
	// same-dir dot-prefixed temp then rename, the way writeMeta does it: a reader must
	// never see a half-written intent, and a torn one is not a state this writer can
	// produce.
	tmp := filepath.Join(dir, ".launch-intent.tmp."+strconv.Itoa(pid))
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, launchIntentPath(ch, alias))
}

// FreshLaunchIntent reports an UNEXPIRED intent for ch/alias and how long ago it was
// written, so a refusal can name its own age instead of saying "try again later".
//
// A marker that cannot be read or parsed reads as ABSENT, and the named limit is
// deliberate: it carries no age, so nothing could ever expire it, and a guard that
// refuses forever on bytes it cannot understand is a wedge with no way out. The
// atomic rename above is what makes that case not arise from this writer.
func FreshLaunchIntent(ch, alias string) (LaunchIntent, time.Duration, bool) {
	var in LaunchIntent
	// screened on the read side too: the path is built from these, and an unscreened
	// alias is a traversal wearing a filename
	if checkStoreName("channel", ch) != nil || checkStoreName("alias", alias) != nil {
		return in, 0, false
	}
	b, err := os.ReadFile(launchIntentPath(ch, alias))
	if err != nil {
		return in, 0, false
	}
	if json.Unmarshal(b, &in) != nil {
		return LaunchIntent{}, 0, false
	}
	ts, err := time.Parse(time.RFC3339, in.TS)
	if err != nil {
		return LaunchIntent{}, 0, false
	}
	age := time.Since(ts)
	if age < 0 {
		age = 0 // a clock that stepped backwards is not evidence the launch is older
	}
	if age >= launchIntentTTL {
		return LaunchIntent{}, 0, false
	}
	return in, age, true
}

// ClearLaunchIntentFor drops ch/alias's intent when sid is the session it was written
// for. Same-sid only: a DIFFERENT session arriving at this name is not the launch that
// was promised, and letting it clear the marker would hand the guard to the one party
// it exists to stop. Best-effort — an intent that outlives its child expires anyway.
func ClearLaunchIntentFor(ch, alias, sid string) {
	if sid == "" {
		return // a session with no id cannot be the one that was launched
	}
	if checkStoreName("channel", ch) != nil || checkStoreName("alias", alias) != nil {
		return
	}
	b, err := os.ReadFile(launchIntentPath(ch, alias))
	if err != nil {
		return
	}
	var in LaunchIntent
	if json.Unmarshal(b, &in) != nil || in.SessionID != sid {
		return
	}
	_ = os.Remove(launchIntentPath(ch, alias))
}

// LaunchIntentExpiry is how long a fresh intent has left, for a message that has to
// tell the operator when to try again.
func LaunchIntentExpiry(age time.Duration) time.Duration {
	return launchIntentTTL - age
}
