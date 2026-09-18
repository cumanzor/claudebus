package client

import "os"

// Native connect is unavailable on Windows; keep the shared package buildable.
func reservationReadFlags() int { return os.O_RDONLY }
