package client

func installSignalGuard() func() int64 { return func() int64 { return 0 } }
