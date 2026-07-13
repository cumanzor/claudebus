package client

import "testing"

func TestIsRemote(t *testing.T) {
	remote := []string{"dev@nuc/mbp", "@nuc", "a@", "dev@nuc"}
	local := []string{"main/fork-1", "main", "/main", "a/b/c", ""}
	for _, r := range remote {
		if !IsRemote(r) {
			t.Errorf("IsRemote(%q) = false, want true", r)
		}
	}
	for _, l := range local {
		if IsRemote(l) {
			t.Errorf("IsRemote(%q) = true, want false", l)
		}
	}
}

func TestParseLocal(t *testing.T) {
	cases := []struct {
		in           string
		ch, al       string
		wantErr      bool
		errSubstring string
	}{
		{"main/fork-1", "main", "fork-1", false, ""},
		{"main", "", "main", false, ""},            // bare alias
		{"/main", "", "main", false, ""},           // empty channel skips validation (quirk)
		{"a/b/c", "", "", true, `bad alias "b/c"`}, // second / lands in the alias
		{"main/", "", "", true, `bad alias ""`},    // empty alias
		{"./x", "", "", true, `bad channel "."`},
		{"x/..", "", "", true, `bad alias ".."`},
		{"", "", "", true, `bad alias ""`},
	}
	for _, c := range cases {
		ch, al, err := ParseLocal(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseLocal(%q) = (%q,%q,nil), want error", c.in, ch, al)
			} else if c.errSubstring != "" && err.Error() != c.errSubstring {
				t.Errorf("ParseLocal(%q) err = %q, want %q", c.in, err.Error(), c.errSubstring)
			}
			continue
		}
		if err != nil || ch != c.ch || al != c.al {
			t.Errorf("ParseLocal(%q) = (%q,%q,%v), want (%q,%q,nil)", c.in, ch, al, err, c.ch, c.al)
		}
	}
}

func TestParseRemote(t *testing.T) {
	cases := []struct {
		in           string
		ch, host, al string
		wantErr      bool
		errSubstring string
	}{
		{"dev@nuc/mbp", "dev", "nuc", "mbp", false, ""},
		{"dev@nuc", "dev", "nuc", "", false, ""},
		{"@nuc/al", "", "nuc", "al", false, ""}, // empty channel accepted client-side
		{"@nuc", "", "nuc", "", false, ""},
		{"dev@nuc/a/b", "", "", "", true, `bad alias "a/b"`},
		{"a@b@c", "", "", "", true, `bad host "b@c"`}, // channel=before FIRST @
		{"dev@", "", "", "", true, `bad host ""`},
		{"@/al", "", "", "", true, `bad host ""`},
	}
	for _, c := range cases {
		ch, host, al, err := ParseRemote(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseRemote(%q) = (%q,%q,%q,nil), want error", c.in, ch, host, al)
			} else if c.errSubstring != "" && err.Error() != c.errSubstring {
				t.Errorf("ParseRemote(%q) err = %q, want %q", c.in, err.Error(), c.errSubstring)
			}
			continue
		}
		if err != nil || ch != c.ch || host != c.host || al != c.al {
			t.Errorf("ParseRemote(%q) = (%q,%q,%q,%v), want (%q,%q,%q,nil)", c.in, ch, host, al, err, c.ch, c.host, c.al)
		}
	}
}

func TestParse(t *testing.T) {
	// remote passes through
	if tg, err := Parse("dev@nuc/mbp", nil); err != nil || !tg.Remote || tg.Channel != "dev" || tg.Host != "nuc" || tg.Alias != "mbp" {
		t.Errorf("Parse(remote) = %+v, %v", tg, err)
	}
	// full local, no resolver needed
	if tg, err := Parse("c/a", nil); err != nil || tg.Remote || tg.Channel != "c" || tg.Alias != "a" {
		t.Errorf("Parse(local) = %+v, %v", tg, err)
	}
	// bare alias resolved via the injected resolver
	resolve := func(al string) (string, bool) {
		if al == "known" {
			return "myced", true
		}
		return "", false
	}
	if tg, err := Parse("known", resolve); err != nil || tg.Channel != "myced" || tg.Alias != "known" {
		t.Errorf("Parse(bare resolved) = %+v, %v", tg, err)
	}
	// bare alias that resolves to nothing -> hard error
	if _, err := Parse("unknown", resolve); err == nil || err.Error() != "use <channel>/<alias>" {
		t.Errorf("Parse(bare unresolved) err = %v, want 'use <channel>/<alias>'", err)
	}
	// bare alias with no resolver -> hard error
	if _, err := Parse("bare", nil); err == nil || err.Error() != "use <channel>/<alias>" {
		t.Errorf("Parse(bare, nil resolver) err = %v", err)
	}
}
