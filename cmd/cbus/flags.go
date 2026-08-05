package main

import (
	"fmt"
	"strings"
)

// The real flag parser behind the frozen verb set. It replaces the ad-hoc per-verb
// loops with one option scanner, and carries the two ruled deltas over bash (both in
// the port-map delta table):
//
//   - a `--` terminator ends option parsing, so a positional (e.g. a message body)
//     may begin with '-' — bash had no way to express that.
//   - trailing junk is an ERROR (noExtra) where bash silently discarded extra args.
//
// Frozen output strings are preserved: the option errors ("missing value for %s",
// "unknown flag %s") and every verb's own usage/die text are unchanged.

// parsedArgs is the result of splitVerbArgs: the valued options seen (name->value),
// the boolean flags seen, and the remaining positionals (after leading options and an
// optional `--`).
type parsedArgs struct {
	opts  map[string]string
	flags map[string]bool
	multi map[string][]string
	pos   []string
}

// has reports a valued option's value and whether it was present (so an explicit
// empty value is distinguishable from an absent one).
func (p parsedArgs) has(name string) (string, bool) {
	v, ok := p.opts[name]
	return v, ok
}

// all returns every occurrence of a valued option, in order — for a repeatable
// flag. opts keeps the last occurrence, so single-value callers are unchanged.
func (p parsedArgs) all(name string) []string { return p.multi[name] }

// splitVerbArgs scans leading `--name value` (names in valued) and `--name` (names in
// bare) options until the first non-option token or a `--` terminator, then treats the
// rest as positionals. strictUnknown decides what an unrecognized `--flag` means: an
// error (auth set) or the start of positionals (send, whose message may contain '-').
func splitVerbArgs(args []string, valued, bare map[string]bool, strictUnknown bool) (parsedArgs, error) {
	p := parsedArgs{opts: map[string]string{}, flags: map[string]bool{}, multi: map[string][]string{}}
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" { // ruled delta: terminator — everything after is positional
			i++
			break
		}
		switch {
		case valued[a]:
			if i+1 >= len(args) {
				return p, fmt.Errorf("missing value for %s", a)
			}
			p.opts[a] = args[i+1]
			p.multi[a] = append(p.multi[a], args[i+1])
			i += 2
		case bare[a]:
			p.flags[a] = true
			i++
		case strictUnknown && strings.HasPrefix(a, "--"):
			return p, fmt.Errorf("unknown flag %s", a)
		default:
			p.pos = args[i:]
			return p, nil
		}
	}
	p.pos = args[i:]
	return p, nil
}

// noExtra enforces the trailing-junk delta: a verb that consumed maxPos positionals
// errors (with its own usage) on anything beyond them, where bash silently discarded.
func noExtra(pos []string, maxPos int, usage string) error {
	if len(pos) > maxPos {
		return fmt.Errorf("%s", usage)
	}
	return nil
}
