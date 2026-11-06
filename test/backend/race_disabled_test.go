//go:build !race

package backend_test

// raceEnabled avoids introducing the detector into ordinary backend test runs.
const raceEnabled = false
