package claude_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/it-bens/cc-port/internal/claude"
)

// FuzzVerifyProjectIdentity asserts the identity guard's three-state outcome
// is deterministic under arbitrary projectPath and cwd byte sequences. One
// synthetic session file is planted per run, then the guard is invoked with
// either an in-set or out-of-set sessionUUID. The oracle:
//
//   - inProjectSet=false → no witness → guard returns nil
//   - inProjectSet=true and cwd==projectPath → match → guard returns nil
//   - inProjectSet=true and cwd!=projectPath → contradiction → guard errors
//
// Catches regressions where a future implementation starts normalising,
// prefix-matching, or otherwise deviating from exact-equality on cwd.
func FuzzVerifyProjectIdentity(f *testing.F) {
	f.Add("/Users/me/foo", "/Users/me/foo", "11111111-1111-1111-1111-111111111111", true)
	f.Add("/Users/me/foo", "/Users/me/bar", "22222222-2222-2222-2222-222222222222", true)
	f.Add("/Users/me/foo", "/Users/me/foo", "33333333-3333-3333-3333-333333333333", false)
	f.Add("/Users/test/Projects/my project", "/Users/test/Projects/my-project",
		"e5f6a7b8-0000-0000-0000-000000000005", true)
	f.Add("", "", "44444444-4444-4444-4444-444444444444", true)

	f.Fuzz(func(t *testing.T, projectPath, cwd, sessionID string, inProjectSet bool) {
		if sessionID == "" {
			return
		}

		tempDir := t.TempDir()
		home := &claude.Home{Dir: tempDir, ConfigFile: tempDir + ".json"}
		if err := os.MkdirAll(home.SessionsDir(), 0o750); err != nil {
			t.Fatalf("create sessions dir: %v", err)
		}

		payload, err := json.Marshal(struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}{SessionID: sessionID, Cwd: cwd})
		if err != nil {
			return
		}

		// json.Marshal replaces invalid UTF-8 bytes with U+FFFD, so the
		// round-tripped values may not equal the inputs. The guard reads
		// from disk, not from the fuzz inputs, so any drift invalidates the
		// oracle below; skip those runs rather than chase phantom mismatches.
		var roundTripped struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}
		if err := json.Unmarshal(payload, &roundTripped); err != nil {
			return
		}
		if roundTripped.SessionID != sessionID || roundTripped.Cwd != cwd {
			return
		}

		sessionFilePath := filepath.Join(home.SessionsDir(), "probe.json")
		if err := os.WriteFile(sessionFilePath, payload, 0o600); err != nil {
			t.Fatalf("write session file: %v", err)
		}

		var sessionUUIDs []string
		if inProjectSet {
			sessionUUIDs = []string{sessionID}
		}

		err = claude.VerifyProjectIdentityForTest(home, projectPath, sessionUUIDs)
		gotError := err != nil
		wantError := inProjectSet && cwd != projectPath

		if wantError != gotError {
			t.Fatalf(
				"outcome mismatch: want error=%v got error=%v (err=%v) (path=%q cwd=%q sid=%q inSet=%v)",
				wantError, gotError, err, projectPath, cwd, sessionID, inProjectSet,
			)
		}
	})
}
