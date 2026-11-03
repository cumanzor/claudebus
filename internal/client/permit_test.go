package client

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const permitTestRepo = "git@github.com:o/r.git"

// permitFixture: dev/lead coordinates a bound seat dev/coder; this process is the lead.
func permitFixture(t *testing.T) {
	t.Helper()
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CBUS_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-lead")
	plantPeer(t, "dev", "lead", "sid-lead")
	plantPeer(t, "dev", "coder", "sid-coder")
	if _, err := recordCoordinator("dev", "coder", "dev/lead", coordinatorDoorApply, "sid-coder"); err != nil {
		t.Fatal(err)
	}
}

func asSession(t *testing.T, sid string) { t.Helper(); t.Setenv("CLAUDE_CODE_SESSION_ID", sid) }

func writeTestPermit(t *testing.T, req PermitRequest) Permit {
	t.Helper()
	if req.Channel == "" {
		req.Channel = "dev"
	}
	if req.Repo == "" {
		req.Repo = permitTestRepo
	}
	if req.Says == "" {
		req.Says = "go ahead"
	}
	p, err := NewPermit(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePermit(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func pushUse(ref string) PermitUse {
	return PermitUse{Channel: "dev", Action: "push-branch", Repo: "https://github.com/o/r", Ref: ref}
}

func wantNotCovered(t *testing.T, u PermitUse, reason string) {
	t.Helper()
	_, err := UsePermit(u)
	var no PermitNotCovered
	if !errors.As(err, &no) {
		t.Fatalf("want not covered, got %v", err)
	}
	if !strings.Contains(no.Error(), reason) {
		t.Fatalf("reason %q does not say %q", no.Error(), reason)
	}
}

func usesOf(t *testing.T, id string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(permitDir("dev"), id+".uses"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func TestNormalizeRepo(t *testing.T) {
	for raw, want := range map[string]string{
		"git@github.com:o/r.git":            "github.com/o/r",
		"github.com:o/r":                    "github.com/o/r",
		"ssh://git@GitHub.com/o/r.git":      "github.com/o/r",
		"ssh://git@github.com:22/o/r":       "github.com/o/r",
		"ssh://git@host.test:2222/o/r":      "host.test:2222/o/r",
		"https://github.com/o/r":            "github.com/o/r",
		"https://github.com/o/r.git/":       "github.com/o/r",
		"https://github.com:443/o/r":        "github.com/o/r",
		"https://user:tok3n@github.com/o/r": "github.com/o/r",
		"http://host.test:8080/g/sub/r.git": "host.test:8080/g/sub/r",
		"https://github.com/O/R":            "github.com/O/R",
		" https://github.com/o/r\n":         "github.com/o/r",
	} {
		if got, err := NormalizeRepo(raw); err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "/srv/git/r.git", "./r", "../r", "file:///srv/r.git", "justaname", "https://github.com", "https://h/a/../b", "ftp://h/o/r",
		"user:s3cret@github.com:o/r.git", "user:pa/ss3cret@github.com:o/r.git", "git@github.com:o/s3cret@r", "https://user:12/ss3cret@github.com/o/r", "git@github.com:o/r?x=s3cret", "git@github.com:o/r#s3cret", "https//user:s3cret@github.com/o/r", "foo bar:s3cret@x",
		"C:/repos/r", `C:\repos\r`, "a/b:o/r", "foo bar:o/r", "ho_st:o/r", "git@host.test:o/r\x1b", "https://host.test/o/r\x07"} {
		if got, err := NormalizeRepo(raw); err == nil {
			t.Errorf("%q: want a refusal, got %q", raw, got)
		}
	}
}

func TestNormalizeRepoDoesNotEchoACredential(t *testing.T) {
	for _, raw := range []string{
		"https://user:s3cret@github.com",
		"user:s3cret@github.com:o/r.git",
		"user:pa/ss3cret@github.com:o/r.git",
		"https://user:12/ss3cret@github.com/o/r",
		"https//user:s3cret@github.com/o/r",
		"foo bar:s3cret@x",
		"git@github.com:o/r?x=s3cret",
		"s3cret",
		"s3cret.host:",
		"https://user:s3cret x@github.com/o/r",
	} {
		if _, err := NormalizeRepo(raw); err == nil || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

// TestPermitWithAnScpCredentialWritesNothing: through the door a coordinator uses.
func TestPermitWithAnScpCredentialWritesNothing(t *testing.T) {
	permitFixture(t)
	_, err := NewPermit(PermitRequest{Channel: "dev", Actions: []string{"draft-pr"}, Repo: "user:s3cret@github.com:o/r.git", Ref: "x", Says: "go"})
	var usage PermitUsageError
	if !errors.As(err, &usage) || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("%v", err)
	}
}

func TestPermitControlBytesDoNotReachTheAudit(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"effort"}, Effort: "scope\x1b[31m\nhere", Says: "says\x1b[2Jthis\x07"})
	if p.Effort != "scope[31m here" || p.Says != "says[2Jthis" {
		t.Fatalf("%q %q", p.Effort, p.Says)
	}
	_, err := NewPermit(PermitRequest{Channel: "dev", Actions: []string{"merge"}, Repo: permitTestRepo, Target: "a\x1bb", Says: "go"})
	var usage PermitUsageError
	if !errors.As(err, &usage) {
		t.Fatalf("a target with a control byte must be refused: %v", err)
	}
}

func TestRefPatternWidth(t *testing.T) {
	for _, ok := range []string{"main", "master", "epic/demo", "epic/demo*", "epic/demo/*", "feat/?x", "m", "ma/*"} {
		if err := checkRefPattern(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"*", "**", "*/*", "?", "?ain", "*x", "m*", "ma*", "mai?", "main*", "mas*", "ma??", "a/**", "x[ab]", `x\y`, "-x", "/x", "x/", "a..b", "a b", ""} {
		if err := checkRefPattern(bad); err == nil {
			t.Errorf("%q: want a refusal", bad)
		}
	}
}

func TestRefMatchStopsAtASlash(t *testing.T) {
	for _, tc := range []struct {
		pattern, ref string
		want         bool
	}{
		{"epic/demo*", "epic/demo", true},
		{"epic/demo*", "epic/demo-1", true},
		{"epic/demo*", "epic/demo/x", false},
		{"epic/demo/*", "epic/demo/x", true},
		{"epic/demo/*", "epic/demo/x/y", false},
		{"epic/demo/*", "epic/demo", false},
		{"main", "main", true},
		{"main", "main2", false},
		{"feat/?", "feat/a", true},
		{"feat/?", "feat/ab", false},
	} {
		if got := permitRefMatches(tc.pattern, tc.ref); got != tc.want {
			t.Errorf("%q vs %q = %v", tc.pattern, tc.ref, got)
		}
	}
}

func TestPermitRequestRefusals(t *testing.T) {
	base := func(edit func(*PermitRequest)) PermitRequest {
		r := PermitRequest{Channel: "dev", Actions: []string{"push-branch"}, Repo: permitTestRepo, Ref: "epic/*", Says: "go"}
		edit(&r)
		return r
	}
	for name, req := range map[string]PermitRequest{
		"no says":   base(func(r *PermitRequest) { r.Says = "  " }),
		"long says": base(func(r *PermitRequest) { r.Says = strings.Repeat("x", permitTextMax+1) }),
		"long effort": base(func(r *PermitRequest) {
			r.Actions, r.Ref, r.Effort = []string{"effort"}, "", strings.Repeat("x", permitTextMax+1)
		}),
		"no action":           base(func(r *PermitRequest) { r.Actions = nil }),
		"unknown class":       base(func(r *PermitRequest) { r.Actions = []string{"*"} }),
		"no ref":              base(func(r *PermitRequest) { r.Ref = "" }),
		"wildcard ref":        base(func(r *PermitRequest) { r.Ref = "*" }),
		"ref reaching main":   base(func(r *PermitRequest) { r.Ref = "m*" }),
		"ref class w/ target": base(func(r *PermitRequest) { r.Target = "x" }),
		"ref class w/ effort": base(func(r *PermitRequest) { r.Effort = "x" }),
		"merge no target":     base(func(r *PermitRequest) { r.Actions, r.Ref = []string{"merge"}, "" }),
		"merge with ref":      base(func(r *PermitRequest) { r.Actions, r.Target = []string{"merge"}, "!1" }),
		"merge wildcard":      base(func(r *PermitRequest) { r.Actions, r.Ref, r.Target = []string{"merge"}, "", "!*" }),
		"merge two words":     base(func(r *PermitRequest) { r.Actions, r.Ref, r.Target = []string{"merge"}, "", "a b" }),
		"long target": base(func(r *PermitRequest) {
			r.Actions, r.Ref, r.Target = []string{"merge"}, "", strings.Repeat("x", permitTargetMax+1)
		}),
		"merge and release":  base(func(r *PermitRequest) { r.Actions, r.Ref, r.Target = []string{"merge", "release"}, "", "!1" }),
		"merge and push":     base(func(r *PermitRequest) { r.Actions, r.Target = []string{"merge", "push-branch"}, "!1" }),
		"effort no text":     base(func(r *PermitRequest) { r.Actions, r.Ref = []string{"effort"}, "" }),
		"effort with ref":    base(func(r *PermitRequest) { r.Actions, r.Effort = []string{"effort"}, "x" }),
		"effort with target": base(func(r *PermitRequest) { r.Actions, r.Ref, r.Effort, r.Target = []string{"effort"}, "", "x", "t" }),
		"effort and push":    base(func(r *PermitRequest) { r.Actions, r.Effort = []string{"effort", "push-branch"}, "x" }),
		"local repo":         base(func(r *PermitRequest) { r.Repo = "/srv/r.git" }),
		"over max ttl":       base(func(r *PermitRequest) { r.TTL = PermitMaxTTL + time.Second }),
		"negative ttl":       base(func(r *PermitRequest) { r.TTL = -time.Second }),
		"relay channel":      base(func(r *PermitRequest) { r.Channel = "dev@server" }),
		"bad channel":        base(func(r *PermitRequest) { r.Channel = "../x" }),
	} {
		t.Run(name, func(t *testing.T) {
			permitFixture(t)
			_, err := NewPermit(req)
			var usage PermitUsageError
			if !errors.As(err, &usage) {
				t.Fatalf("want a usage refusal, got %v", err)
			}
		})
	}
}

func TestPermitDefaultsAndMaxTTL(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"push-branch", "draft-pr", "push-branch"}, Ref: "refs/heads/epic/*"})
	if got := p.ExpiresAt.Sub(p.CreatedAt); got != PermitDefaultTTL {
		t.Errorf("default ttl = %s", got)
	}
	if p.Ref != "epic/*" || p.Repo != "github.com/o/r" || p.Writer != "dev/lead" || p.WriterSession != "sid-lead" || len(p.Actions) != 2 {
		t.Errorf("%+v", p)
	}
	max := writeTestPermit(t, PermitRequest{Actions: []string{"draft-pr"}, Ref: "x", TTL: PermitMaxTTL})
	if got := max.ExpiresAt.Sub(max.CreatedAt); got != PermitMaxTTL {
		t.Errorf("max ttl = %s", got)
	}
}

func TestPermitStoresNoCredential(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"draft-pr"}, Ref: "x", Repo: "https://user:s3cret@github.com/o/r"})
	b, err := os.ReadFile(filepath.Join(permitDir("dev"), p.ID+".json"))
	if err != nil || strings.Contains(string(b), "s3cret") {
		t.Fatalf("%s %v", b, err)
	}
}

func TestPermitWriteIsOnce(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"draft-pr"}, Ref: "x"})
	if err := WritePermit(p); err == nil {
		t.Fatal("an id collision must fail, not overwrite")
	}
}

func TestPermitWriterMustCoordinateSomeone(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T){
		"the seat itself": func(t *testing.T) { asSession(t, "sid-coder") },
		"not on channel":  func(t *testing.T) { asSession(t, "sid-other") },
		"no records at all": func(t *testing.T) {
			if err := os.RemoveAll(filepath.Join(CBUSDir(), ".coordinators")); err != nil {
				t.Fatal(err)
			}
		},
		"corrupt record": func(t *testing.T) {
			if err := os.WriteFile(coordinatorRecordPath("dev", "coder"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			permitFixture(t)
			setup(t)
			_, err := NewPermit(PermitRequest{Channel: "dev", Actions: []string{"draft-pr"}, Repo: permitTestRepo, Ref: "x", Says: "go"})
			var usage PermitUsageError
			if err == nil || errors.As(err, &usage) {
				t.Fatalf("want a store refusal, got %v", err)
			}
		})
	}
}

func TestPermitWriterOffTheChannelIsToldSo(t *testing.T) {
	permitFixture(t)
	asSession(t, "sid-other")
	_, err := NewPermit(PermitRequest{Channel: "dev", Actions: []string{"draft-pr"}, Repo: permitTestRepo, Ref: "x", Says: "go"})
	if err == nil || !strings.Contains(err.Error(), "not on dev") {
		t.Fatalf("%v", err)
	}
}

func TestPermitUnboundRecordLetsTheCoordinatorWrite(t *testing.T) {
	permitFixture(t)
	if _, err := recordCoordinator("dev", "coder", "dev/lead", coordinatorDoorApply, ""); err != nil {
		t.Fatal(err)
	}
	writeTestPermit(t, PermitRequest{Actions: []string{"draft-pr"}, Ref: "x"})
}

func TestPermitUseCoversAndCounts(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"push-branch", "pr-update"}, Ref: "epic/demo*"})
	asSession(t, "sid-coder")
	v, err := UsePermit(pushUse("epic/demo-1"))
	if err != nil || v.ID != p.ID || v.Uses != 1 {
		t.Fatalf("%+v %v", v, err)
	}
	v, err = UsePermit(PermitUse{Channel: "dev", Action: "pr-update", Repo: permitTestRepo, Ref: "refs/heads/epic/demo-2"})
	if err != nil || v.Uses != 2 {
		t.Fatalf("%+v %v", v, err)
	}
	if got := usesOf(t, p.ID); got != 2 {
		t.Fatalf("use lines = %d", got)
	}
	b, _ := os.ReadFile(filepath.Join(permitDir("dev"), p.ID+".uses"))
	if !strings.Contains(string(b), `"by":"dev/coder"`) || !strings.Contains(string(b), `"session":"sid-coder"`) || !strings.Contains(string(b), `"ref":"epic/demo-2"`) {
		t.Fatalf("%s", b)
	}
}

func TestPermitSurvivesARestoredSeatSession(t *testing.T) {
	permitFixture(t)
	writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "epic/*"})
	if _, err := recordCoordinator("dev", "coder", "dev/lead", coordinatorDoorApply, "sid-next"); err != nil {
		t.Fatal(err)
	}
	plantPeer(t, "dev", "coder", "sid-next")
	asSession(t, "sid-next")
	if _, err := UsePermit(pushUse("epic/x")); err != nil {
		t.Fatal(err)
	}
}

func TestPermitUseRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		setup  func(t *testing.T, p Permit)
		use    PermitUse
		reason string
	}{
		"ref mismatch":     {use: pushUse("feat/x"), reason: "covers ref epic/*, not feat/x"},
		"ref past a slash": {use: pushUse("epic/a/b"), reason: "covers ref epic/*"},
		"repo mismatch":    {use: PermitUse{Channel: "dev", Action: "push-branch", Repo: "https://github.com/o/other", Ref: "epic/x"}, reason: "this repo is github.com/o/other"},
		"local repo":       {use: PermitUse{Channel: "dev", Action: "push-branch", Repo: "/srv/r.git", Ref: "epic/x"}, reason: "local path"},
		"another class":    {use: PermitUse{Channel: "dev", Action: "draft-pr", Repo: permitTestRepo, Ref: "epic/x"}, reason: "no permit for draft-pr"},
		"expired": {use: pushUse("epic/x"), reason: "expired", setup: func(t *testing.T, p Permit) {
			permitNow = func() time.Time { return p.ExpiresAt }
			t.Cleanup(func() { permitNow = time.Now })
		}},
		"revoked": {use: pushUse("epic/x"), reason: "was revoked", setup: func(t *testing.T, p Permit) {
			if _, err := RevokePermit(p.ID, "dev/lead"); err != nil {
				t.Fatal(err)
			}
		}},
		"no record": {use: pushUse("epic/x"), reason: "no coordinator record", setup: func(t *testing.T, _ Permit) {
			if err := os.Remove(coordinatorRecordPath("dev", "coder")); err != nil {
				t.Fatal(err)
			}
		}},
		"unbound record": {use: pushUse("epic/x"), reason: "no coordinator record", setup: func(t *testing.T, _ Permit) {
			if _, err := recordCoordinator("dev", "coder", "dev/lead", coordinatorDoorApply, ""); err != nil {
				t.Fatal(err)
			}
		}},
		"wrong writer": {use: pushUse("epic/x"), reason: "written by dev/lead, your coordinator is dev/other", setup: func(t *testing.T, _ Permit) {
			if _, err := recordCoordinator("dev", "coder", "dev/other", coordinatorDoorApply, "sid-coder"); err != nil {
				t.Fatal(err)
			}
		}},
		"not on channel": {use: PermitUse{Channel: "other", Action: "push-branch", Repo: permitTestRepo, Ref: "epic/x"}, reason: "not on other"},
		"effort is read": {use: PermitUse{Channel: "dev", Action: "effort", Repo: permitTestRepo}, reason: "read with cbus permits"},
	} {
		t.Run(name, func(t *testing.T) {
			permitFixture(t)
			p := writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "epic/*"})
			if tc.setup != nil {
				tc.setup(t, p)
			}
			asSession(t, "sid-coder")
			wantNotCovered(t, tc.use, tc.reason)
			if got := usesOf(t, p.ID); got != 0 {
				t.Fatalf("a refused use wrote %d lines", got)
			}
		})
	}
}

func TestPermitUseBadRequests(t *testing.T) {
	permitFixture(t)
	asSession(t, "sid-coder")
	for name, u := range map[string]PermitUse{
		"unknown class":   {Channel: "dev", Action: "anything", Repo: permitTestRepo, Ref: "x"},
		"no ref":          {Channel: "dev", Action: "push-branch", Repo: permitTestRepo},
		"wildcard ref":    {Channel: "dev", Action: "push-branch", Repo: permitTestRepo, Ref: "epic/*"},
		"merge no target": {Channel: "dev", Action: "merge", Repo: permitTestRepo},
		"relay channel":   {Channel: "dev@server", Action: "push-branch", Repo: permitTestRepo, Ref: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			var usage PermitUsageError
			if _, err := UsePermit(u); !errors.As(err, &usage) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestPermitMergeIsBoundToItsTarget(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"merge"}, Target: "!381"})
	if p.Ref != "" {
		t.Fatalf("%+v", p)
	}
	asSession(t, "sid-coder")
	merge := func(target string) PermitUse {
		return PermitUse{Channel: "dev", Action: "merge", Repo: permitTestRepo, Target: target}
	}
	wantNotCovered(t, merge("!382"), "names target !381, not !382")
	wantNotCovered(t, merge("!38"), "names target !381")
	if v, err := UsePermit(merge("!381")); err != nil || v.Uses != 1 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestPermitUseFindsACoveringOneBesideMisses(t *testing.T) {
	permitFixture(t)
	writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "other/*"})
	good := writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "epic/*"})
	writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "third/*"})
	asSession(t, "sid-coder")
	if v, err := UsePermit(pushUse("epic/x")); err != nil || v.ID != good.ID {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestPermitReasonIsTheNearestMiss(t *testing.T) {
	permitFixture(t)
	writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "other/*"})
	revoked := writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "epic/*"})
	if _, err := RevokePermit(revoked.ID, "dev/lead"); err != nil {
		t.Fatal(err)
	}
	asSession(t, "sid-coder")
	wantNotCovered(t, pushUse("epic/x"), "covers ref other/*")
}

func TestPermitUnreadableUseLogRefuses(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "epic/*"})
	if err := os.Mkdir(filepath.Join(permitDir("dev"), p.ID+".uses"), 0o700); err != nil {
		t.Fatal(err)
	}
	asSession(t, "sid-coder")
	wantNotCovered(t, pushUse("epic/x"), "could not record the use")
}

func TestPermitForeignFilesAreNotPermits(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"push-branch"}, Ref: "epic/*"})
	path := filepath.Join(permitDir("dev"), p.ID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"newer version": strings.Replace(string(b), `"v": 1`, `"v": 2`, 1),
		"torn":          string(b[:len(b)/2]),
		"oversized":     string(b) + strings.Repeat(" ", coordinatorRecordMax),
		"other channel": strings.Replace(string(b), `"channel": "dev"`, `"channel": "else"`, 1),
		"no writer":     strings.Replace(string(b), `"writer": "dev/lead"`, `"writer": ""`, 1),
	}
	asSession(t, "sid-coder")
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if body == string(b) {
				t.Fatal("the fixture did not change the file")
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			wantNotCovered(t, pushUse("epic/x"), "no permit for push-branch")
		})
	}
	badName := filepath.Join(permitDir("dev"), "p-short.json")
	if err := os.WriteFile(badName, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if len(permitsOnChannel("dev")) != 0 {
		t.Fatal("a misnamed file read as a permit")
	}
}

func TestPermitsListing(t *testing.T) {
	permitFixture(t)
	mine := writeTestPermit(t, PermitRequest{Actions: []string{"effort"}, Effort: "M1 to M5"})
	gone := writeTestPermit(t, PermitRequest{Actions: []string{"draft-pr"}, Ref: "x"})
	if _, err := RevokePermit(gone.ID, "dev/lead"); err != nil {
		t.Fatal(err)
	}
	asSession(t, "sid-coder")
	listed := ListPermits(ResolveSelf(), false)
	if len(listed) != 1 || listed[0].ID != mine.ID || listed[0].Effort != "M1 to M5" {
		t.Fatalf("%+v", listed)
	}
	if all := ListPermits(nil, true); len(all) != 2 {
		t.Fatalf("%+v", all)
	}
	if _, err := recordCoordinator("dev", "coder", "dev/other", coordinatorDoorApply, "sid-coder"); err != nil {
		t.Fatal(err)
	}
	if got := ListPermits(ResolveSelf(), false); len(got) != 0 {
		t.Fatalf("another coordinator's permit must not show: %+v", got)
	}
	asSession(t, "sid-lead")
	if got := ListPermits(ResolveSelf(), false); len(got) != 1 {
		t.Fatalf("the writer sees its own: %+v", got)
	}
}

func TestPermitRevoke(t *testing.T) {
	permitFixture(t)
	p := writeTestPermit(t, PermitRequest{Actions: []string{"draft-pr"}, Ref: "x"})
	asSession(t, "")
	v, err := RevokePermit(p.ID, "first")
	if err != nil || v.State != PermitRevoked || v.RevokedBy != "first" {
		t.Fatalf("%+v %v", v, err)
	}
	v, err = RevokePermit(p.ID, "second")
	if err != nil || v.RevokedBy != "first" {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := RevokePermit("p-0000000000", "x"); err == nil {
		t.Fatal("unknown id")
	}
	if _, err := RevokePermit("../x", "x"); err == nil {
		t.Fatal("bad id")
	}
}
