package client

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// resumedSids maps session ids to the pid of a process on this machine whose argv
// resumes them (`--resume <sid>`, `-r <sid>`, `--resume=<sid>`). It sees the session
// the bus cannot: one resumed by hand or from another launcher that never joined.
// A fresh session carries no sid in its argv, so it stays invisible here; the
// meta-based liveSids covers it once it joins. Windows has no argv read and
// reports nothing.
var resumedSids = func() map[string]int {
	if runtime.GOOS == "windows" {
		return nil
	}
	out, err := exec.Command("ps", "-axww", "-o", "pid=,args=").Output()
	if err != nil {
		return nil
	}
	return parseResumedSids(string(out), os.Getpid())
}

func parseResumedSids(ps string, self int) map[string]int {
	held := map[string]int{}
	for _, line := range strings.Split(ps, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid == self {
			continue
		}
		args := f[1:]
		for i, a := range args {
			sid := ""
			switch {
			case (a == "--resume" || a == "-r") && i+1 < len(args):
				sid = args[i+1]
			case strings.HasPrefix(a, "--resume="):
				sid = strings.TrimPrefix(a, "--resume=")
			}
			if validSessionUUID(sid) {
				if prev, ok := held[sid]; !ok || pid < prev {
					held[sid] = pid
				}
			}
		}
	}
	return held
}

// validSessionUUID: 8-4-4-4-12 hex, the shape of a Claude session id. A loose match
// would let an unrelated `-r` flag claim a session.
func validSessionUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
	}
	return true
}

// addResumedSids folds process-held sessions into a liveSids map. A bus holder
// already named wins: its address is the more useful pointer.
func addResumedSids(live map[string]string, held map[string]int) {
	for sid, pid := range held {
		if _, ok := live[sid]; !ok {
			live[sid] = fmt.Sprintf("pid %d (a process resuming it, not on the bus)", pid)
		}
	}
}
