package client

// grantProcLookup: grants are refused on windows before the walk matters.
func grantProcLookup() func(int) (procRecord, bool) { return procLookup() }
