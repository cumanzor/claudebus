// Package conformance holds the Phase 0 wire conformance rig. It builds and runs
// the REAL cbus-relay binary (std-lib only) and drives its HTTP + ws endpoints
// against the shared core wire structs, proving core.SendReq / core.PeersResponse /
// core.Message match the live relay contract end-to-end. Test-only; no exported API.
package conformance
