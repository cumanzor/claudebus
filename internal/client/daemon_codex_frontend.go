package client

import "time"

func managedCodexConnection(c *ConnectionState) bool {
	if daemonHarness(c.Harness) != daemonHarnessCodex {
		return false
	}
	if c.Consumer != nil && c.Consumer.Managed {
		return true
	}
	// recognize journals written before frontend identities were persisted.
	pid := c.Config.RuntimePID
	if c.Consumer != nil && c.Consumer.PID > 0 {
		pid = c.Consumer.PID
	}
	if pid <= 0 {
		return false
	}
	argv, err := procArgs(pid)
	return err == nil && managedCodexProcess(argv)
}

// a shared backend drains its native queue even after the terminal exits.
// retain new mail in the bus inbox until this exact thread has a verified TUI.
func (d *busDaemon) managedCodexDeliveryReady(c *ConnectionState) (bool, error) {
	next := cloneConnection(c)
	if next.Consumer == nil {
		next.Consumer = &consumerObservation{}
	}
	next.Consumer.Managed = true
	p, probeErr := d.consumerProbe(next)
	if probeErr != nil {
		p.State, p.Detail = "unknown", probeErr.Error()
	}
	if err := d.applyConsumerProbe(next, p, true); err != nil {
		return false, err
	}
	if consumerChanged(c.Consumer, next.Consumer) || c.PresenceSequence != next.PresenceSequence {
		if err := d.save(next); err != nil {
			return false, err
		}
	}
	*c = *next
	ready := probeErr == nil && p.State == "online" && (!p.Managed || p.Frontend != nil)
	if !ready {
		d.setRetry(c.ID, time.Now().Add(5*time.Second))
	}
	return ready, nil
}
