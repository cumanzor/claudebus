package client

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
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

// ClaimLaunchIntent atomically claims the launch of sid as ch/alias. claimed=true
// means this process is the one launch permitted right now and may fork.
//
// The channel dir is created if absent: resuming into a channel that no longer exists
// is legal (a reboot takes the whole store's live state with it), and an empty channel
// dir is a harmless artifact. Callers are channel/alias-general on purpose — the same
// window exists for any launcher that forks a session and waits for it to join
// (cbus-yca), and adopting this should be a call, not surgery.
//
// The claim IS the write, and the write is os.Link, which fails with EEXIST when the
// path is taken. That is the whole correctness argument: a read-then-write guard is
// check-then-act, and temp+rename is LAST-writer-wins, so N racers all read absent,
// all rename, and all fork — measured, 2-4 of 16 forking per run. Link is
// FIRST-writer-wins and the syscall itself reports which one you were, so no racer
// ever has to infer its own outcome from a later read.
//
// claimed=false with err==nil is a refusal: `in`/`age` describe the claim in the way,
// or `in` is zero when the loss was to a racer whose claim is no longer readable.
func ClaimLaunchIntent(ch, alias, sid string) (in LaunchIntent, age time.Duration, claimed bool, err error) {
	if err := checkStoreName("channel", ch); err != nil {
		return LaunchIntent{}, 0, false, err
	}
	if err := checkStoreName("alias", alias); err != nil {
		return LaunchIntent{}, 0, false, err
	}
	dir := filepath.Join(CBUSDir(), ch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return LaunchIntent{}, 0, false, err
	}
	tmp, mine, err := writeIntentTmp(dir, ch, alias, sid)
	if err != nil {
		return LaunchIntent{}, 0, false, err
	}
	defer os.Remove(tmp)
	path := launchIntentPath(ch, alias)

	// Two passes at most: the claim, and one reclaim of an expired corpse. A loser of
	// the reclaim refuses rather than spinning — a retry loop here would be a lock
	// with extra steps, and the caller's remedy (wait, or come back after the TTL) is
	// the same either way.
	for attempt := 0; attempt < 2; attempt++ {
		switch err := os.Link(tmp, path); {
		case err == nil:
			return mine, 0, true, nil
		case !errors.Is(err, fs.ErrExist):
			return LaunchIntent{}, 0, false, err
		}
		if held, heldAge, ok := FreshLaunchIntent(ch, alias); ok {
			return held, heldAge, false, nil // someone live holds it: refuse against THEIR facts
		}
		if attempt > 0 {
			return LaunchIntent{}, 0, false, nil // reclaimed, then lost the free slot to a racer
		}
		if reclaimCorpse(dir, path, tmp) {
			return mine, 0, true, nil // we replaced the corpse with our own marker, in one step
		}
	}
	return LaunchIntent{}, 0, false, nil
}

// reclaimCorpse replaces an EXPIRED or unreadable marker with the caller's own, and
// reports whether the caller thereby owns the claim. It is the only path that ever
// displaces another process's marker.
//
// Two rules make it safe, and the first one is the one that took a bounced milestone
// to learn: the corpse is never REMOVED, it is overwritten by a single atomic rename.
// Any reclaim that unlinks first — rename-aside, remove-then-link — makes the path
// momentarily absent, and absence is exactly the signal every other racer is waiting
// for, so a racer links into the hole and forks alongside the reclaimer. Measured: the
// rename-aside version forked 2 of 16.
//
// The second rule handles the reclaimers themselves. Overwriting is last-writer-wins,
// so N reclaimers would all "succeed" — they are serialized by an exclusive link on a
// reclaim token, and the losers refuse rather than retry. The winner holds that token
// across three syscalls and releases it by defer.
func reclaimCorpse(dir, path, tmp string) bool {
	token := path + ".reclaim"
	// A token outliving the TTL was leaked by a process killed inside the section
	// below — it is held for microseconds otherwise. Removing it cannot displace a
	// live reclaimer: a live one stamped its token NOW, and a stamp cannot read as
	// three minutes old.
	if in, ok := readIntent(token); ok && ageOf(in) >= launchIntentTTL {
		_ = os.Remove(token)
	}
	ttmp, _, err := writeIntentTmp(dir, "", "", "") // a token, never read as a claim: only its stamp matters
	if err != nil {
		return false
	}
	defer os.Remove(ttmp)
	if os.Link(ttmp, token) != nil {
		return false // another reclaimer owns the corpse; our caller refuses against whatever it leaves
	}
	defer os.Remove(token)
	// Sole reclaimer, and every claimer is still blocked by the corpse sitting in the
	// path, so re-reading here is safe: nothing can have claimed it, only cleared it.
	if in, ok := readIntent(path); ok && ageOf(in) < launchIntentTTL {
		return false // it went fresh under us (its child joined, someone claimed): refuse against it
	}
	return os.Rename(tmp, path) == nil
}

// writeIntentTmp stages the marker's full bytes under a name nothing reads, so the
// linked-into-place file is complete the instant it becomes visible.
func writeIntentTmp(dir, ch, alias, sid string) (string, LaunchIntent, error) {
	pid := os.Getpid()
	start, _ := procStartTime(pid) // provenance only: a probe that cannot answer must not stop the guard
	in := LaunchIntent{Channel: ch, Alias: alias, SessionID: sid, Pid: pid, ProcStart: start, TS: Now()}
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return "", LaunchIntent{}, err
	}
	tmp := filepath.Join(dir, ".launch-intent.tmp."+intentSuffix())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", LaunchIntent{}, err
	}
	return tmp, in, nil
}

// intentSuffix names a scratch file no concurrent claim can collide with. The pid
// alone is not enough: two claims can race inside ONE process (the barrier probe does
// exactly that), and a shared temp name would have them overwriting each other's
// bytes before either linked.
func intentSuffix() string {
	return strconv.Itoa(os.Getpid()) + "." + strconv.FormatUint(atomic.AddUint64(&intentSeq, 1), 36)
}

var intentSeq uint64

// FreshLaunchIntent reports an UNEXPIRED intent for ch/alias and how long ago it was
// written, so a refusal can name its own age instead of saying "try again later".
//
// A marker that cannot be read or parsed reads as ABSENT, and the named limit is
// deliberate: it carries no age, so nothing could ever expire it, and a guard that
// refuses forever on bytes it cannot understand is a wedge with no way out. The
// atomic rename above is what makes that case not arise from this writer.
func FreshLaunchIntent(ch, alias string) (LaunchIntent, time.Duration, bool) {
	// screened on the read side too: the path is built from these, and an unscreened
	// alias is a traversal wearing a filename
	if checkStoreName("channel", ch) != nil || checkStoreName("alias", alias) != nil {
		return LaunchIntent{}, 0, false
	}
	in, ok := readIntent(launchIntentPath(ch, alias))
	if !ok {
		return LaunchIntent{}, 0, false
	}
	age := ageOf(in)
	if age >= launchIntentTTL {
		return LaunchIntent{}, 0, false
	}
	return in, age, true
}

// readIntent parses a marker at an exact path. Unreadable and unparseable are the
// same answer — no intent — and the reclaim path treats both alike.
func readIntent(path string) (LaunchIntent, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return LaunchIntent{}, false
	}
	var in LaunchIntent
	if json.Unmarshal(b, &in) != nil {
		return LaunchIntent{}, false
	}
	if _, err := time.Parse(time.RFC3339, in.TS); err != nil {
		return LaunchIntent{}, false // no usable age is the same as no intent
	}
	return in, true
}

// ageOf is how long ago a marker was written. A marker readIntent accepted always has
// a parseable stamp, so the error branch here is unreachable and answers EXPIRED,
// which is the direction that cannot wedge a channel.
func ageOf(in LaunchIntent) time.Duration {
	ts, err := time.Parse(time.RFC3339, in.TS)
	if err != nil {
		return launchIntentTTL
	}
	age := time.Since(ts)
	if age < 0 {
		age = 0 // a clock that stepped backwards is not evidence the launch is older
	}
	return age
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
