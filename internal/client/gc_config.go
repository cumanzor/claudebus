package client

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	gcDefaultGrace       = 15 * time.Minute
	gcDefaultInactive    = 14 * 24 * time.Hour
	gcDefaultArchiveKeep = 30 * 24 * time.Hour
	gcFirstPassDelay     = time.Minute
	gcPassInterval       = 5 * time.Minute
)

// GCSettings is the collection policy, from the environment over defaults:
// CBUS_GC=off stops the daemon's automatic passes, CBUS_GC_GRACE and
// CBUS_GC_INACTIVE set the two limits, CBUS_GC_ARCHIVE sets how long an
// archived record is kept.
type GCSettings struct {
	Auto        bool
	Limits      GCLimits
	ArchiveKeep time.Duration
}

func LoadGCSettings() (GCSettings, error) {
	s := GCSettings{Auto: true, Limits: GCLimits{Grace: gcDefaultGrace, Inactive: gcDefaultInactive}, ArchiveKeep: gcDefaultArchiveKeep}
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("CBUS_GC"))); v {
	case "", "on":
	case "off":
		s.Auto = false
	default:
		return s, fmt.Errorf("CBUS_GC=%q: use on or off", v)
	}
	for _, e := range []struct {
		name string
		into *time.Duration
	}{{"CBUS_GC_GRACE", &s.Limits.Grace}, {"CBUS_GC_INACTIVE", &s.Limits.Inactive}, {"CBUS_GC_ARCHIVE", &s.ArchiveKeep}} {
		if v := strings.TrimSpace(os.Getenv(e.name)); v != "" {
			d, err := ParseGCDuration(v)
			if err != nil {
				return s, fmt.Errorf("%s: %w", e.name, err)
			}
			*e.into = d
		}
	}
	return s, nil
}

// ParseGCDuration accepts Go durations plus a whole-day form, since these limits
// are naturally counted in days.
func ParseGCDuration(v string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q is not a positive number of days", v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a positive duration", v)
	}
	return d, nil
}
