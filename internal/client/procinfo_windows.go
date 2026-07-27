package client

import (
	"errors"
	"strings"
	"syscall"
	"unsafe"
)

// windows process inspection via the Toolhelp snapshot and the process handle APIs.
// Same contract as procinfo_darwin.go and procinfo_linux.go: syscall wrappers with no
// policy, and the start-token format lives in starttime.go so the writer and the
// prober cannot drift.

// _PROCESS_QUERY_LIMITED_INFORMATION is absent from package syscall. It is the
// narrowest right that answers both liveness and creation time, and unlike
// PROCESS_QUERY_INFORMATION it is granted for processes at a higher integrity level.
const _PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

var errNoProcArgs = errors.New("procArgs unimplemented on windows")

// procArgs is deliberately unimplemented, and returns an error rather than an empty
// string so the argv liveness clause reads DEAD instead of reading "no arguments".
//
// On darwin argv[0] is the PRIMARY harness identity and comm is the broken fallback,
// because the bun-compiled Claude CLI sets its accounting name to its version string
// (see ownerFromPid). Windows inverts that: the Toolhelp image name is the name on
// disk and a process cannot rename itself that way, so procParent already answers the
// only question the argv clause was covering. Reading a real argv here would take
// NtQueryInformationProcess plus a PEB walk with per-architecture offsets, which buys
// nothing while the image name answers.
func procArgs(pid int) (string, error) {
	return "", errNoProcArgs
}

// procStartTime is the (pid,starttime) witness: the process creation FILETIME, handed
// to the shared composer. Syscall wrapper only. An error (no such pid, access denied,
// zero creation time) makes the caller read the listener DEAD — a probe that cannot
// answer never answers alive.
func procStartTime(pid int) (string, error) {
	creation, err := procCreationFiletime(pid)
	if err != nil {
		return "", err
	}
	return windowsStartToken(creation.HighDateTime, creation.LowDateTime)
}

// procCreationTicks is the same creation time as one comparable integer, for the
// walk's parent-age check. Ordering only — it is never rendered and never parsed as a
// clock, which is why the witness TOKEN is composed in starttime.go instead of here.
func procCreationTicks(pid int) (uint64, error) {
	creation, err := procCreationFiletime(pid)
	if err != nil {
		return 0, err
	}
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime), nil
}

func procCreationFiletime(pid int) (syscall.Filetime, error) {
	var creation, exit, kernel, user syscall.Filetime
	if pid <= 0 {
		return creation, syscall.ESRCH
	}
	h, err := syscall.OpenProcess(_PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return creation, err
	}
	defer syscall.CloseHandle(h)
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return creation, err
	}
	return creation, nil
}

// procWalkRoot is the pid the ancestor walk stops above. There is no pid 1 here: the
// tree bottoms out at pid 0, the System Idle process. The unix terminator would never
// fire, leaving the depth backstop to end every walk — a mechanism doing a safety
// net's job, and silently, since nothing crashes when it happens.
const procWalkRoot = 0

// procLookup reads process records for the harness walk from ONE Toolhelp snapshot,
// taken on first use and reused for the rest of the climb. Creation times are filled in
// per pid on demand: the snapshot does not carry them, and opening every process on the
// box to collect times the walk will never consult is not worth a join.
//
// Argv stays empty. procArgs is unimplemented here on purpose (see above), so the walk's
// argv clause never matches and the image name carries the identity alone.
func procLookup() func(int) (procRecord, bool) {
	var table map[int]procRecord
	return func(pid int) (procRecord, bool) {
		if table == nil {
			table = snapshotProcs()
		}
		rec, ok := table[pid]
		if !ok {
			return procRecord{}, false
		}
		if rec.Created == 0 {
			if ticks, err := procCreationTicks(pid); err == nil {
				rec.Created = ticks
				table[pid] = rec
			}
		}
		return rec, true
	}
}

// snapshotProcs walks one Toolhelp process snapshot into pid-keyed records. An
// unreadable snapshot yields an empty table, which makes every lookup miss and the walk
// return "" — never guessed.
func snapshotProcs() map[int]procRecord {
	out := map[int]procRecord{}
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer syscall.CloseHandle(snap)
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		out[int(e.ProcessID)] = procRecord{
			PPid: int(e.ParentProcessID),
			Comm: imageBase(syscall.UTF16ToString(e.ExeFile[:])),
		}
	}
	return out
}

// procParent returns pid's image name (normalized by imageBase) and its parent pid,
// both from one Toolhelp snapshot entry.
//
// KNOWN HAZARD: windows does not invalidate ParentProcessID when the parent exits, and
// pids are reused, so an ancestor walk built on this can climb into an unrelated process
// that now wears the recorded parent pid. Not theoretical on the target machine — 15
// live processes there carried a parent id naming a pid that no longer existed.
//
// HarnessName is PROTECTED: it climbs via harnessWalk, whose ancestryPlausible rejects a
// parent created after its own child. ownerFromPid in marker.go is NOT — it walks
// procParent directly with no such check. That is deferred rather than overlooked: it
// feeds the close.go owner walk, which is out of phase, and until then its result lands
// in a ledger field rather than behind a signal, so a wrong answer is a mislabeled record
// and not a stranger being killed. Closing it means routing that walk through the same
// check, not adding a second one here.
func procParent(pid int) (comm string, ppid int, err error) {
	if pid <= 0 {
		return "", 0, syscall.ESRCH
	}
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", 0, err
	}
	defer syscall.CloseHandle(snap)
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		if int(e.ProcessID) == pid {
			return imageBase(syscall.UTF16ToString(e.ExeFile[:])), int(e.ParentProcessID), nil
		}
	}
	return "", 0, syscall.ESRCH
}

// imageBase normalizes a windows image name to the shape the shared harness matcher
// expects: a bare basename with no extension. isHarnessComm matches "claude" exactly
// and "claude-" by prefix, so an unstripped "claude.exe" would miss on every join and
// every arm. Normalizing on the side that has the deviation keeps commBase and the
// unix platforms untouched.
func imageBase(name string) string {
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
		name = name[:len(name)-4]
	}
	return name
}

// procZombie reports false, and the reason is NOT that windows lacks the state.
// Windows has the structural analogue: a terminated process whose kernel object is
// retained because someone still holds a handle to it, so its pid still opens. That
// is precisely the hole procZombie exists to plug on linux, where a zombie keeps its
// original starttime and kill(0) still succeeds. Here it is closed UPSTREAM instead —
// pidAlive waits on the handle, and a terminated process is signaled — so by the time
// this is consulted there is nothing left for it to catch.
func procZombie(pid int) bool {
	return false
}

// pidAlive reports whether pid names a running process.
//
// The handle is waited on rather than asked for its exit code. GetExitCodeProcess
// reports STILL_ACTIVE, which is 259 — a value drawn from the same space as a real
// exit code, so a process that legitimately exits with 259 reads alive forever and no
// amount of care at the call site can separate the two. A wait has no sentinel:
// WAIT_TIMEOUT means the handle is unsignaled, which means still running.
//
// Opening can fail for two different reasons and only one of them means dead. Access
// denied proves the process EXISTS (the EPERM branch of the unix implementation);
// anything else reads dead, which is the house posture that a probe unable to answer
// never answers alive.
//
// That access-denied branch is written to ship UNEXERCISED. It cannot be reached from
// the test harness, whose ssh session holds an elevated token and can open anything,
// while the sessions that actually run cbus hold ordinary ones. It fires the first time
// a peer probes a pid it does not own, with nothing having gone before it — hence
// errors.Is rather than a bare comparison, so a wrapped errno cannot silently turn a
// live stranger into a corpse.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE|_PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	ev, err := syscall.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(syscall.WAIT_TIMEOUT)
}
