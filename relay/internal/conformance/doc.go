// Package conformance holds the wire conformance rig. It builds and runs the REAL
// cbus-relay binary (std-lib only) and drives its HTTP + ws endpoints against the
// shared core wire structs, proving core.SendReq / core.PeersResponse /
// core.Message match the live relay contract end-to-end. Test-only; no exported API.
//
// CACHING GOTCHA: the relay is built and exec'd at RUNTIME (via `go build` +
// os/exec), NOT imported, so it is not part of this test binary. `go test` would
// therefore serve a cached PASS after a relay source change, testing a stale
// binary. The rig defends against this by reading every relay/ and internal/core
// .go file at test start (trackSources) — those file opens are recorded in the
// test cache, so any relay change invalidates the cached result. If you ever
// remove that call, validate relay changes with `go test -count=1`.
package conformance
