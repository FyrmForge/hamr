package devserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// walksFilePath is the on-disk path hamr dev writes its port-walk record
// to, and `hamr env` / `hamr sync` read from. Lives under .hamr/ alongside
// the per-compose-entry override yaml so the existing .gitignore rule for
// .hamr/ already covers it.
const walksFilePath = ".hamr/walks.json"

// portShiftRecord is the public schema of one entry in walks.json. Same
// shape as the internal portShift used by walkComposeServices, but with
// JSON tags and a "kind" so the file can carry proxy/app shifts in the
// same array as compose shifts without separate top-level fields.
type portShiftRecord struct {
	Kind          string `json:"kind"`                     // "proxy", "app", or "compose"
	ComposeName   string `json:"compose_name,omitempty"`   // "[[dev.docker_compose]].name", compose-only
	Service       string `json:"service,omitempty"`        // compose service name, compose-only
	ContainerPort int    `json:"container_port,omitempty"` // compose service container port, compose-only
	HostIP        string `json:"host_ip,omitempty"`        // compose host_ip if non-empty, compose-only
	Old           int    `json:"old"`                      // pre-walk port
	New           int    `json:"new"`                      // post-walk port (== Old when no shift)
}

// walksFile is the on-disk shape. A flat shifts array keeps adding new
// kinds (e.g. one day a sidecar service we walk independently) cheap —
// no schema migration, just a new "kind" tag.
type walksFile struct {
	Shifts []portShiftRecord `json:"shifts"`
}

// writeWalks persists shifts to dir/.hamr/walks.json. Empty shifts list
// removes any stale file so a subsequent `hamr env` exits cleanly with
// no output instead of replaying yesterday's walks. Unconditional write
// (no skip when nothing changed) means readers always see fresh data
// after every dev start — important when compose stack state changes
// between runs.
func writeWalks(dir string, shifts []portShiftRecord) error {
	path := filepath.Join(dir, walksFilePath)
	if len(shifts) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale walks file: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(walksFile{Shifts: shifts}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal walks: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// readWalks loads walks.json from dir. Missing file returns (nil, nil)
// so callers can treat "no walks recorded" the same as "no shifts to
// apply" — common case when port_walk is off or every port was free.
func readWalks(dir string) ([]portShiftRecord, error) {
	path := filepath.Join(dir, walksFilePath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f walksFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f.Shifts, nil
}

// buildWalkRecords assembles the walks.json shift list from the various
// sources hamr dev tracks (proxy, app, compose). Each source contributes
// at most one record per shifted port; identity entries (e.g. proxy not
// walked) are skipped so readers can treat "no record" as "no shift".
//
// composeShiftToEntry maps an index into composeShifts back to the
// `[[dev.docker_compose]].name` of the entry that produced it — needed
// because portShift carries the service name but not the compose entry
// name, and consumers want both for disambiguation when multiple entries
// publish the same service-internal port.
func buildWalkRecords(originalProxyPort, actualProxyPort, originalAppPort, actualAppPort int, composeShifts []portShift, composeShiftToEntry map[int]string) []portShiftRecord {
	var out []portShiftRecord
	if originalProxyPort != 0 && actualProxyPort != 0 && originalProxyPort != actualProxyPort {
		out = append(out, portShiftRecord{
			Kind: "proxy",
			Old:  originalProxyPort,
			New:  actualProxyPort,
		})
	}
	if originalAppPort != 0 && actualAppPort != 0 && originalAppPort != actualAppPort {
		out = append(out, portShiftRecord{
			Kind: "app",
			Old:  originalAppPort,
			New:  actualAppPort,
		})
	}
	for i, s := range composeShifts {
		if s.Old == s.New {
			continue
		}
		out = append(out, portShiftRecord{
			Kind:        "compose",
			ComposeName: composeShiftToEntry[i],
			Service:     s.Service,
			HostIP:      s.HostIP,
			Old:         s.Old,
			New:         s.New,
		})
	}
	return out
}

// shiftsToMap collapses a record list into the old→new lookup the
// rewrite engine wants. Identity entries (Old == New, recorded so users
// can see "tried to walk, didn't need to") drop out — they'd no-op the
// regex anyway, and including them would bloat the rewrite scan.
func shiftsToMap(records []portShiftRecord) portShifts {
	if len(records) == 0 {
		return nil
	}
	m := make(portShifts, len(records))
	for _, r := range records {
		if r.Old == r.New {
			continue
		}
		m[r.Old] = r.New
	}
	return m
}
