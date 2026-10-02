package client

import (
	"sort"
	"testing"
	"time"
)

// schedulerTick mirrors RunDaemon: one tick, then a resume per freed slot,
// until no worker is running.
func schedulerTick(t *testing.T, d *busDaemon) {
	t.Helper()
	d.schedule()
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case <-d.slotFreed:
			d.continueSchedule()
		case <-time.After(20 * time.Millisecond):
			if len(d.slots) == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler tick did not settle")
		}
	}
}

func TestDaemonSchedulerLatencyIsIndependentOfRecordCount(t *testing.T) {
	const count = 48
	d, cs, qs := schedulerFixture(t, count)
	ids := make([]string, len(cs))
	byID := map[string]int{}
	for i, c := range cs {
		ids[i] = c.ID
		byID[c.ID] = i
	}
	sort.Strings(ids)
	d.mu.Lock()
	// the record a rotating scheduler reaches last
	target := byID[ids[(d.rotation+len(ids)-1)%len(ids)]]
	d.mu.Unlock()
	appendDaemonMessage(t, cs[target], "", "behind the rotation")
	for tick := 1; tick <= 2; tick++ {
		schedulerTick(t, d)
		if len(qs[target].observedCalls()) == 1 {
			return
		}
	}
	t.Fatalf("a peer with mail waited more than 2 ticks behind %d registered connections", count)
}
