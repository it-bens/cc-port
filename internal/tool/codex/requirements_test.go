package codex

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

// TestMain points the machine-wide Codex locations at a scratch directory
// before any test runs, so no test in this package reads the real
// /etc/codex or /Library/Managed Preferences through Open.
func TestMain(m *testing.M) {
	scratch, err := os.MkdirTemp("", "codex-machine-locations")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create machine-location scratch directory: %v\n", err)
		os.Exit(1)
	}
	systemConfigDir = filepath.Join(scratch, "etc-codex")
	managedPreferencesDir = filepath.Join(scratch, "Managed Preferences")
	code := m.Run()
	_ = os.RemoveAll(scratch)
	os.Exit(code)
}

var fixtureNow = time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)

// machineFixture is one scratch machine: a codex home plus the system
// directory and managed-preference locations managedSources names.
type machineFixture struct {
	codexHome     string
	systemDir     string
	userPlist     string
	computerPlist string
}

func newMachineFixture(t *testing.T) machineFixture {
	t.Helper()
	root := t.TempDir()
	codexHome := filepath.Join(root, "dotcodex")
	require.NoError(t, os.MkdirAll(codexHome, 0o750))
	return machineFixture{
		codexHome:     codexHome,
		systemDir:     filepath.Join(root, "etc-codex"),
		userPlist:     filepath.Join(root, "Managed Preferences", "fixture-user", "com.openai.codex.plist"),
		computerPlist: filepath.Join(root, "Managed Preferences", "com.openai.codex.plist"),
	}
}

func (fixture machineFixture) sources() *managedSources {
	return &managedSources{
		systemRequirementsFile:  fixture.systemRequirementsFile(),
		systemConfigFile:        fixture.systemConfigFile(),
		managedConfigFile:       fixture.managedConfigFile(),
		managedPreferencePlists: []string{fixture.userPlist, fixture.computerPlist},
		cloudCacheFile:          fixture.cloudCachePath(),
		authFile:                filepath.Join(fixture.codexHome, codexAuthFile),
	}
}

func (fixture machineFixture) systemRequirementsFile() string {
	return filepath.Join(fixture.systemDir, systemRequirementsFileName)
}

func (fixture machineFixture) systemConfigFile() string {
	return filepath.Join(fixture.systemDir, configTOMLFileName)
}

func (fixture machineFixture) managedConfigFile() string {
	return filepath.Join(fixture.systemDir, managedConfigFileName)
}

func (fixture machineFixture) cloudCachePath() string {
	return filepath.Join(fixture.codexHome, cloudConfigBundleCacheFile)
}

func absentManagedSources(t *testing.T, codexHome string) *managedSources {
	t.Helper()
	scratch := t.TempDir()
	return &managedSources{
		systemRequirementsFile:  filepath.Join(scratch, systemRequirementsFileName),
		systemConfigFile:        filepath.Join(scratch, configTOMLFileName),
		managedConfigFile:       filepath.Join(scratch, managedConfigFileName),
		managedPreferencePlists: []string{filepath.Join(scratch, "com.openai.codex.plist")},
		cloudCacheFile:          filepath.Join(codexHome, cloudConfigBundleCacheFile),
		authFile:                filepath.Join(codexHome, codexAuthFile),
	}
}

func writeFixtureFile(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

type fixtureFragment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Contents string `json:"contents"`
}

// fixtureBundle is a cloud config bundle in Codex's field order.
type fixtureBundle struct {
	ConfigTOML struct {
		EnterpriseManaged []fixtureFragment `json:"enterprise_managed"`
	} `json:"config_toml"`
	RequirementsTOML struct {
		EnterpriseManaged []fixtureFragment `json:"enterprise_managed"`
	} `json:"requirements_toml"`
}

func requirementsBundle(fragments ...fixtureFragment) fixtureBundle {
	var bundle fixtureBundle
	bundle.ConfigTOML.EnterpriseManaged = []fixtureFragment{}
	bundle.RequirementsTOML.EnterpriseManaged = append([]fixtureFragment{}, fragments...)
	return bundle
}

func configBundle(fragments ...fixtureFragment) fixtureBundle {
	var bundle fixtureBundle
	bundle.ConfigTOML.EnterpriseManaged = append([]fixtureFragment{}, fragments...)
	bundle.RequirementsTOML.EnterpriseManaged = []fixtureFragment{}
	return bundle
}

// writeCloudCache writes a cache the way Codex's CloudConfigBundleCache::save
// does: the signed payload in declaration order, signed with Codex's HMAC key
// over its compact JSON.
func writeCloudCache(t *testing.T, path string, expiresAt time.Time, bundle fixtureBundle) {
	t.Helper()
	payload, err := json.Marshal(struct {
		Version       int           `json:"version"`
		CachedAt      time.Time     `json:"cached_at"`
		ExpiresAt     time.Time     `json:"expires_at"`
		ChatGPTUserID string        `json:"chatgpt_user_id"`
		AccountID     string        `json:"account_id"`
		Bundle        fixtureBundle `json:"bundle"`
	}{cloudConfigBundleCacheVersion, expiresAt.Add(-time.Hour), expiresAt, "fixture-user", "fixture-account", bundle})
	require.NoError(t, err)
	mac := hmac.New(sha256.New, cloudConfigBundleCacheHMACKey)
	mac.Write(payload)
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	var cache bytes.Buffer
	cache.WriteString(`{"signed_payload":`)
	require.NoError(t, json.Indent(&cache, payload, "", "  "))
	cache.WriteString(`,"signature":"` + signature + `"}`)
	writeFixtureFile(t, path, cache.Bytes())
}

// syntheticIDToken builds an unsigned JWT whose payload is claims, the only
// part cc-port decodes.
func syntheticIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"none"}`)) + "." + encode(payload) + "." + encode([]byte("fixture-signature"))
}

// writeChatGPTAuth writes a ChatGPT-mode auth.json signed in as
// fixture-user on plan, whose tokens carry accountID.
func writeChatGPTAuth(t *testing.T, codexHome, plan, accountID string) {
	t.Helper()
	writeChatGPTAuthClaims(t, codexHome, map[string]any{"chatgpt_plan_type": plan, "chatgpt_user_id": "fixture-user"}, accountID)
}

// writeChatGPTAuthClaims writes a ChatGPT-mode auth.json whose id_token
// carries authClaims as its https://api.openai.com/auth claim.
func writeChatGPTAuthClaims(t *testing.T, codexHome string, authClaims map[string]any, accountID string) {
	t.Helper()
	idToken := syntheticIDToken(t, map[string]any{"https://api.openai.com/auth": authClaims})
	auth, err := json.Marshal(map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token": idToken, "access_token": "fixture-access", "refresh_token": "fixture-refresh", "account_id": accountID,
		},
	})
	require.NoError(t, err)
	writeFixtureFile(t, filepath.Join(codexHome, codexAuthFile), auth)
}

// signInToCloudAccount signs codexHome in as the enterprise account
// writeCloudCache caches bundles for.
func signInToCloudAccount(t *testing.T, codexHome string) {
	t.Helper()
	writeChatGPTAuth(t, codexHome, "enterprise", "fixture-account")
}

// xmlManagedPreferencesPlist is the XML form of a forced-preference plist
// carrying toml base64-encoded under key, next to an unrelated integer key
// the reader must skip.
func xmlManagedPreferencesPlist(key, toml string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadVersion</key>
	<integer>1</integer>
	<key>` + key + `</key>
	<string>` + base64.StdEncoding.EncodeToString([]byte(toml)) + `</string>
</dict>
</plist>
`)
}

type plistEntry struct {
	key   string
	value any // string or int
}

// binaryPlistFixture encodes entries as a bplist00 dictionary with two-byte
// offsets and one-byte object references.
func binaryPlistFixture(t *testing.T, entries ...plistEntry) []byte {
	t.Helper()
	encodeLength := func(objectType byte, count int) []byte {
		if count < 15 {
			return []byte{objectType<<4 | byte(count)} //nolint:gosec // G115: count < 15
		}
		require.Less(t, count, 1<<16)
		return []byte{objectType<<4 | 0x0F, 0x11, byte(count >> 8), byte(count)} //nolint:gosec // G115: count < 1<<16, required above
	}
	encodeString := func(value string) []byte {
		ascii := true
		for _, r := range value {
			if r > 0x7F {
				ascii = false
			}
		}
		if ascii {
			return append(encodeLength(binaryPlistTypeASCIIString, len(value)), value...)
		}
		units := utf16.Encode([]rune(value))
		encoded := encodeLength(binaryPlistTypeUTF16String, len(units))
		for _, unit := range units {
			encoded = binary.BigEndian.AppendUint16(encoded, unit)
		}
		return encoded
	}

	dict := encodeLength(binaryPlistTypeDict, len(entries))
	for index := range entries {
		dict = append(dict, byte(1+index))
	}
	for index := range entries {
		dict = append(dict, byte(1+len(entries)+index)) //nolint:gosec // G115: fixtures stay far below 256 objects
	}
	objects := [][]byte{dict}
	for _, entry := range entries {
		objects = append(objects, encodeString(entry.key))
	}
	for _, entry := range entries {
		switch value := entry.value.(type) {
		case string:
			objects = append(objects, encodeString(value))
		case int:
			objects = append(objects, []byte{binaryPlistTypeInt << 4, byte(value)}) //nolint:gosec // G115: fixture integers fit one byte
		default:
			t.Fatalf("unsupported plist fixture value %T", value)
		}
	}

	data := []byte("bplist00")
	var offsets []int
	for _, object := range objects {
		offsets = append(offsets, len(data))
		data = append(data, object...)
	}
	offsetTable := len(data)
	for _, offset := range offsets {
		data = binary.BigEndian.AppendUint16(data, uint16(offset)) //nolint:gosec // G115: fixtures stay far below 64 KiB
	}
	trailer := make([]byte, binaryPlistTrailerLength)
	trailer[6], trailer[7] = 2, 1
	binary.BigEndian.PutUint64(trailer[8:16], uint64(len(objects)))
	binary.BigEndian.PutUint64(trailer[24:32], uint64(offsetTable))
	return append(data, trailer...)
}

// machineLayers is one scratch machine's sqlite_home settings, each empty
// when that source is absent.
type machineLayers struct {
	systemRequirements string
	cloudRequirement   string
	mdmRequirements    string
	managedConfig      string
	mdmConfig          string
	userConfig         string
	cloudConfig        string
	systemConfig       string
	environment        string
}

func (layers *machineLayers) write(t *testing.T, fixture machineFixture) func(string) string {
	t.Helper()
	sqliteHome := func(value string) []byte { return []byte(`sqlite_home = "` + value + `"`) }
	if layers.systemRequirements != "" {
		writeFixtureFile(t, fixture.systemRequirementsFile(), sqliteHome(layers.systemRequirements))
	}
	if layers.systemConfig != "" {
		writeFixtureFile(t, fixture.systemConfigFile(), sqliteHome(layers.systemConfig))
	}
	if layers.managedConfig != "" {
		writeFixtureFile(t, fixture.managedConfigFile(), sqliteHome(layers.managedConfig))
	}
	if layers.userConfig != "" {
		writeFixtureFile(t, filepath.Join(fixture.codexHome, configTOMLFileName), sqliteHome(layers.userConfig))
	}
	if layers.cloudRequirement != "" || layers.cloudConfig != "" {
		var bundle fixtureBundle
		bundle.ConfigTOML.EnterpriseManaged = []fixtureFragment{}
		bundle.RequirementsTOML.EnterpriseManaged = []fixtureFragment{}
		if layers.cloudConfig != "" {
			bundle.ConfigTOML.EnterpriseManaged = []fixtureFragment{
				{ID: "storage-config", Name: "Storage config", Contents: string(sqliteHome(layers.cloudConfig))},
			}
		}
		if layers.cloudRequirement != "" {
			bundle.RequirementsTOML.EnterpriseManaged = []fixtureFragment{
				{ID: "storage-policy", Name: "Storage policy", Contents: string(sqliteHome(layers.cloudRequirement))},
			}
		}
		signInToCloudAccount(t, fixture.codexHome)
		writeCloudCache(t, fixture.cloudCachePath(), fixtureNow.Add(time.Hour), bundle)
	}
	if layers.mdmRequirements != "" || layers.mdmConfig != "" {
		var entries []plistEntry
		if layers.mdmRequirements != "" {
			entries = append(entries, plistEntry{
				key:   managedPreferencesRequirementsKey,
				value: base64.StdEncoding.EncodeToString(sqliteHome(layers.mdmRequirements)),
			})
		}
		if layers.mdmConfig != "" {
			entries = append(entries, plistEntry{
				key:   managedPreferencesConfigKey,
				value: base64.StdEncoding.EncodeToString(sqliteHome(layers.mdmConfig)),
			})
		}
		writeFixtureFile(t, fixture.computerPlist, binaryPlistFixture(t, entries...))
	}
	return fakeGetenv(map[string]string{sqliteHomeEnv: layers.environment})
}

func TestResolveSQLiteDirPrecedence(t *testing.T) {
	cases := []struct {
		name     string
		layers   machineLayers
		wantDir  string
		wantTier sqliteHomeTier
	}{
		{
			name: "system requirement outranks every config layer and the environment",
			layers: machineLayers{
				systemRequirements: "/Users/test/sqlite/system-requirement",
				mdmConfig:          "/Users/test/sqlite/mdm-config",
				userConfig:         "/Users/test/sqlite/user-config",
				environment:        "/Users/test/sqlite/environment",
			},
			wantDir:  "/Users/test/sqlite/system-requirement",
			wantTier: sqliteHomeFromRequirement,
		},
		{
			name: "cloud requirement outranks the system requirement",
			layers: machineLayers{
				systemRequirements: "/Users/test/sqlite/system-requirement",
				cloudRequirement:   "/Users/test/sqlite/cloud-requirement",
			},
			wantDir:  "/Users/test/sqlite/cloud-requirement",
			wantTier: sqliteHomeFromRequirement,
		},
		{
			name: "managed-preference requirement outranks the cloud requirement",
			layers: machineLayers{
				cloudRequirement: "/Users/test/sqlite/cloud-requirement",
				mdmRequirements:  "/Users/test/sqlite/mdm-requirement",
			},
			wantDir:  "/Users/test/sqlite/mdm-requirement",
			wantTier: sqliteHomeFromRequirement,
		},
		{
			name: "managed-preference config outranks managed_config.toml",
			layers: machineLayers{
				managedConfig: "/Users/test/sqlite/managed-config",
				mdmConfig:     "/Users/test/sqlite/mdm-config",
			},
			wantDir:  "/Users/test/sqlite/mdm-config",
			wantTier: sqliteHomeFromManagedConfig,
		},
		{
			name: "managed_config.toml outranks config.toml",
			layers: machineLayers{
				managedConfig: "/Users/test/sqlite/managed-config",
				userConfig:    "/Users/test/sqlite/user-config",
			},
			wantDir:  "/Users/test/sqlite/managed-config",
			wantTier: sqliteHomeFromManagedConfig,
		},
		{
			name: "config.toml outranks cloud config",
			layers: machineLayers{
				userConfig:  "/Users/test/sqlite/user-config",
				cloudConfig: "/Users/test/sqlite/cloud-config",
			},
			wantDir:  "/Users/test/sqlite/user-config",
			wantTier: sqliteHomeFromConfigTOML,
		},
		{
			name: "cloud config outranks the system config.toml",
			layers: machineLayers{
				cloudConfig:  "/Users/test/sqlite/cloud-config",
				systemConfig: "/Users/test/sqlite/system-config",
			},
			wantDir:  "/Users/test/sqlite/cloud-config",
			wantTier: sqliteHomeFromMachineConfig,
		},
		{
			name: "the system config.toml outranks the environment",
			layers: machineLayers{
				systemConfig: "/Users/test/sqlite/system-config",
				environment:  "/Users/test/sqlite/environment",
			},
			wantDir:  "/Users/test/sqlite/system-config",
			wantTier: sqliteHomeFromMachineConfig,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			getenv := testCase.layers.write(t, fixture)

			resolution, err := resolveSQLiteDir(fixture.codexHome, getenv, fixture.sources(), fixtureNow)

			require.NoError(t, err)
			assert.Equal(t, testCase.wantDir, resolution.dir)
			assert.Equal(t, testCase.wantTier, resolution.source.tier)
		})
	}
}

func TestResolveSQLiteDirTakesTheFirstCloudFragmentDeclaringSQLiteHome(t *testing.T) {
	fixture := newMachineFixture(t)
	signInToCloudAccount(t, fixture.codexHome)
	writeCloudCache(t, fixture.cloudCachePath(), fixtureNow.Add(time.Hour), requirementsBundle(
		fixtureFragment{ID: "approvals-policy", Name: "Approvals policy", Contents: `allowed_approval_policies = ["never"]`},
		fixtureFragment{ID: "storage-policy", Name: "Storage policy", Contents: `sqlite_home = "/Users/test/sqlite/storage"`},
		fixtureFragment{ID: "fallback-policy", Name: "Fallback policy", Contents: `sqlite_home = "/Users/test/sqlite/fallback"`},
	))

	resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/sqlite/storage", resolution.dir)
}

func TestResolveSQLiteDirLetsUserLevelManagedPreferencesShadowComputerLevel(t *testing.T) {
	fixture := newMachineFixture(t)
	writeFixtureFile(t, fixture.userPlist,
		xmlManagedPreferencesPlist(managedPreferencesRequirementsKey, `sqlite_home = "/Users/test/sqlite/user-managed"`))
	writeFixtureFile(t, fixture.computerPlist,
		xmlManagedPreferencesPlist(managedPreferencesRequirementsKey, `sqlite_home = "/Users/test/sqlite/computer-managed"`))

	resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/sqlite/user-managed", resolution.dir)
}

func TestResolveSQLiteDirUsesComputerLevelPlistWhenUserLevelLacksTheKey(t *testing.T) {
	fixture := newMachineFixture(t)
	writeFixtureFile(t, fixture.userPlist, binaryPlistFixture(t, plistEntry{key: "PayloadVersion", value: 1}))
	writeFixtureFile(t, fixture.computerPlist,
		xmlManagedPreferencesPlist(managedPreferencesRequirementsKey, `sqlite_home = "/Users/test/sqlite/computer-managed"`))

	resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/sqlite/computer-managed", resolution.dir)
}

func TestPlistTopLevelStringDecodesUTF16BinaryStrings(t *testing.T) {
	data := binaryPlistFixture(t, plistEntry{key: "label", value: "Zürich Büro"})

	value, found, err := plistTopLevelString(data, "label")

	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "Zürich Büro", value)
}

func TestResolveSQLiteDirResolvesRelativeMachineValues(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, fixture machineFixture)
		wantDir func(fixture machineFixture) string
	}{
		{
			name: "system requirements value resolves against the system directory",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.systemRequirementsFile(), []byte(`sqlite_home = "state"`))
			},
			wantDir: func(fixture machineFixture) string { return filepath.Join(fixture.systemDir, "state") },
		},
		{
			name: "managed_config.toml value resolves against the system directory",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.managedConfigFile(), []byte(`sqlite_home = "state"`))
			},
			wantDir: func(fixture machineFixture) string { return filepath.Join(fixture.systemDir, "state") },
		},
		{
			name: "cloud config value resolves against the codex home",
			arrange: func(t *testing.T, fixture machineFixture) {
				signInToCloudAccount(t, fixture.codexHome)
				writeCloudCache(t, fixture.cloudCachePath(), fixtureNow.Add(time.Hour), configBundle(
					fixtureFragment{ID: "storage-config", Name: "Storage config", Contents: `sqlite_home = "state"`}))
			},
			wantDir: func(fixture machineFixture) string { return filepath.Join(fixture.codexHome, "state") },
		},
		{
			name: "managed-preference config value resolves against the codex home",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.computerPlist, xmlManagedPreferencesPlist(managedPreferencesConfigKey, `sqlite_home = "state"`))
			},
			wantDir: func(fixture machineFixture) string { return filepath.Join(fixture.codexHome, "state") },
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			testCase.arrange(t, fixture)

			resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.NoError(t, err)
			assert.Equal(t, testCase.wantDir(fixture), resolution.dir)
		})
	}
}

func TestResolveSQLiteDirWarnsAboutExpiredCloudCacheAndIgnoresItsValue(t *testing.T) {
	fixture := newMachineFixture(t)
	writeFixtureFile(t, fixture.systemRequirementsFile(), []byte(`sqlite_home = "/Users/test/sqlite/system"`))
	signInToCloudAccount(t, fixture.codexHome)
	writeCloudCache(t, fixture.cloudCachePath(), fixtureNow.Add(-time.Hour), requirementsBundle(
		fixtureFragment{ID: "storage-policy", Name: "Storage policy", Contents: `sqlite_home = "/Users/test/sqlite/cloud"`}))

	resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/sqlite/system", resolution.dir)
	require.Len(t, resolution.warnings, 1)
	assert.Contains(t, resolution.warnings[0], "cloud-managed sqlite_home could not be checked")
	assert.Contains(t, resolution.warnings[0], fixture.cloudCachePath())
}

func TestResolveSQLiteDirWarnsAboutMissingCloudCacheOnlyWhenCloudConfigApplies(t *testing.T) {
	cases := []struct {
		name         string
		signIn       func(t *testing.T, codexHome string)
		wantWarnings int
	}{
		{name: "eligible ChatGPT account", signIn: signInToCloudAccount, wantWarnings: 1},
		{
			name: "API-key auth",
			signIn: func(t *testing.T, codexHome string) {
				writeFixtureFile(t, filepath.Join(codexHome, codexAuthFile), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"fixture-key"}`))
			},
			wantWarnings: 0,
		},
		{name: "no auth.json", signIn: func(*testing.T, string) {}, wantWarnings: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			testCase.signIn(t, fixture.codexHome)

			resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.NoError(t, err)
			assert.Len(t, resolution.warnings, testCase.wantWarnings)
		})
	}
}

// TestResolveSQLiteDirAppliesCodexCloudAuthGate covers every branch of the
// gate Codex runs before it uses the cache: an auth mode or plan that never
// gets cloud config ignores even an unreadable cache, an account mismatch or
// incomplete identity makes Codex refetch, and token-backed modes cc-port
// cannot resolve offline warn.
func TestResolveSQLiteDirAppliesCodexCloudAuthGate(t *testing.T) {
	cases := []struct {
		name         string
		signIn       func(t *testing.T, codexHome string)
		cache        func(t *testing.T, path string)
		wantDir      func(codexHome string) string
		wantWarnings int
	}{
		{
			name: "API-key auth never reads the cache",
			signIn: func(t *testing.T, codexHome string) {
				writeFixtureFile(t, filepath.Join(codexHome, codexAuthFile), []byte(`{"OPENAI_API_KEY":"fixture-key"}`))
			},
			cache:   func(t *testing.T, path string) { writeFixtureFile(t, path, []byte("{")) },
			wantDir: func(codexHome string) string { return codexHome },
		},
		{
			name: "Bedrock API-key auth never reads the cache",
			signIn: func(t *testing.T, codexHome string) {
				writeFixtureFile(t, filepath.Join(codexHome, codexAuthFile),
					[]byte(`{"bedrock_api_key":{"api_key":"fixture-key","region":"us-east-1"}}`))
			},
			cache:   func(t *testing.T, path string) { writeFixtureFile(t, path, []byte("{")) },
			wantDir: func(codexHome string) string { return codexHome },
		},
		{
			name: "Bedrock access-keys auth never reads the cache",
			signIn: func(t *testing.T, codexHome string) {
				writeFixtureFile(t, filepath.Join(codexHome, codexAuthFile),
					[]byte(`{"bedrock_access_keys":{"access_key_id":"fixture-id","secret_access_key":"fixture-secret"}}`))
			},
			cache:   func(t *testing.T, path string) { writeFixtureFile(t, path, []byte("{")) },
			wantDir: func(codexHome string) string { return codexHome },
		},
		{
			name: "a plan without cloud config never reads the cache",
			signIn: func(t *testing.T, codexHome string) {
				writeChatGPTAuth(t, codexHome, "plus", "fixture-account")
			},
			cache:   func(t *testing.T, path string) { writeFixtureFile(t, path, []byte("{")) },
			wantDir: func(codexHome string) string { return codexHome },
		},
		{
			name: "ChatGPT auth without token data never reads the cache",
			signIn: func(t *testing.T, codexHome string) {
				writeFixtureFile(t, filepath.Join(codexHome, codexAuthFile), []byte(`{"auth_mode":"chatgpt"}`))
			},
			cache:   func(t *testing.T, path string) { writeFixtureFile(t, path, []byte("{")) },
			wantDir: func(codexHome string) string { return codexHome },
		},
		{
			name: "an eligible plan alias applies the matching cache",
			signIn: func(t *testing.T, codexHome string) {
				writeChatGPTAuth(t, codexHome, "education", "fixture-account")
			},
			cache:   writeFreshCloudRequirement,
			wantDir: func(string) string { return "/Users/test/sqlite/cloud" },
		},
		{
			name: "a cache for another account is skipped with a warning",
			signIn: func(t *testing.T, codexHome string) {
				writeChatGPTAuth(t, codexHome, "enterprise", "other-account")
			},
			cache:        writeFreshCloudRequirement,
			wantDir:      func(codexHome string) string { return codexHome },
			wantWarnings: 1,
		},
		{
			name: "an id_token without a user id is skipped with a warning",
			signIn: func(t *testing.T, codexHome string) {
				writeChatGPTAuthClaims(t, codexHome, map[string]any{"chatgpt_plan_type": "enterprise"}, "fixture-account")
			},
			cache:        func(t *testing.T, path string) { writeFixtureFile(t, path, []byte("{")) },
			wantDir:      func(codexHome string) string { return codexHome },
			wantWarnings: 1,
		},
		{
			name: "personal-access-token auth cannot be decided offline and warns",
			signIn: func(t *testing.T, codexHome string) {
				writeFixtureFile(t, filepath.Join(codexHome, codexAuthFile), []byte(`{"personal_access_token":"fixture-token"}`))
			},
			cache:        writeFreshCloudRequirement,
			wantDir:      func(codexHome string) string { return codexHome },
			wantWarnings: 1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			testCase.signIn(t, fixture.codexHome)
			testCase.cache(t, fixture.cloudCachePath())

			resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.NoError(t, err)
			assert.Equal(t, testCase.wantDir(fixture.codexHome), resolution.dir)
			assert.Len(t, resolution.warnings, testCase.wantWarnings)
		})
	}
}

func writeFreshCloudRequirement(t *testing.T, path string) {
	t.Helper()
	writeCloudCache(t, path, fixtureNow.Add(time.Hour), requirementsBundle(
		fixtureFragment{ID: "storage-policy", Name: "Storage policy", Contents: `sqlite_home = "/Users/test/sqlite/cloud"`}))
}

// TestResolveSQLiteDirWarnsWhenCodexCannotLoadAuthJSON covers auth.json
// files Codex's deserializer or loader rejects: each leaves Codex with no
// sign-in cc-port can reason about, so resolution warns, derives nothing, and
// never echoes a value from the file.
func TestResolveSQLiteDirWarnsWhenCodexCannotLoadAuthJSON(t *testing.T) {
	chatgptAuth := func(t *testing.T, idToken string, extra map[string]any) []byte {
		t.Helper()
		auth := map[string]any{
			"auth_mode": "chatgpt",
			"tokens": map[string]any{
				"id_token": idToken, "access_token": "secret-marker", "refresh_token": "secret-marker", "account_id": "secret-marker",
			},
		}
		maps.Copy(auth, extra)
		encoded, err := json.Marshal(auth)
		require.NoError(t, err)
		return encoded
	}
	eligibleToken := func(t *testing.T) string {
		return syntheticIDToken(t, map[string]any{
			"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "enterprise", "chatgpt_user_id": "secret-marker"},
		})
	}
	cases := []struct {
		name string
		auth func(t *testing.T) []byte
	}{
		{name: "id_token is not a JWT", auth: func(t *testing.T) []byte { return chatgptAuth(t, "secret-marker", nil) }},
		{name: "id_token payload is not base64url", auth: func(t *testing.T) []byte {
			return chatgptAuth(t, "header.secret-marker!.signature", nil)
		}},
		{name: "id_token payload is not JSON", auth: func(t *testing.T) []byte {
			return chatgptAuth(t, "header."+base64.RawURLEncoding.EncodeToString([]byte("secret-marker"))+".signature", nil)
		}},
		{name: "id_token plan claim is not a string", auth: func(t *testing.T) []byte {
			return chatgptAuth(t, syntheticIDToken(t, map[string]any{
				"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": 7, "chatgpt_user_id": "secret-marker"},
			}), nil)
		}},
		{name: "id_token fedramp claim is null", auth: func(t *testing.T) []byte {
			return chatgptAuth(t, syntheticIDToken(t, map[string]any{
				"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "enterprise", "chatgpt_account_is_fedramp": nil},
			}), nil)
		}},
		{name: "last_refresh is not a timestamp", auth: func(t *testing.T) []byte {
			return chatgptAuth(t, eligibleToken(t), map[string]any{"last_refresh": "secret-marker"})
		}},
		{name: "OPENAI_API_KEY is not a string", auth: func(t *testing.T) []byte {
			return chatgptAuth(t, eligibleToken(t), map[string]any{"OPENAI_API_KEY": 7})
		}},
		{name: "unknown auth_mode", auth: func(*testing.T) []byte { return []byte(`{"auth_mode":"secret-marker"}`) }},
		{name: "API-key mode without a key", auth: func(*testing.T) []byte { return []byte(`{"auth_mode":"apikey"}`) }},
		{name: "Bedrock API-key mode without its key", auth: func(*testing.T) []byte { return []byte(`{"auth_mode":"bedrockApiKey"}`) }},
		{name: "Bedrock access-keys mode without its keys", auth: func(*testing.T) []byte {
			return []byte(`{"auth_mode":"bedrockAccessKeys"}`)
		}},
		{name: "personal-access-token mode without a token", auth: func(*testing.T) []byte {
			return []byte(`{"auth_mode":"personalAccessToken"}`)
		}},
		{name: "bedrock_api_key without a region", auth: func(*testing.T) []byte {
			return []byte(`{"bedrock_api_key":{"api_key":"secret-marker"}}`)
		}},
		{name: "agent identity record without its fedramp flag", auth: func(*testing.T) []byte {
			return []byte(`{"auth_mode":"agentIdentity","agent_identity":{"agent_runtime_id":"secret-marker",` +
				`"agent_private_key":"secret-marker","account_id":"fixture-account","chatgpt_user_id":"fixture-user",` +
				`"plan_type":"enterprise","task_id":"fixture-task"}}`)
		}},
		{name: "duplicate key", auth: func(t *testing.T) []byte {
			return append(bytes.TrimSuffix(chatgptAuth(t, eligibleToken(t), nil), []byte("}")), []byte(`,"auth_mode":"apikey"}`)...)
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			writeFixtureFile(t, filepath.Join(fixture.codexHome, codexAuthFile), testCase.auth(t))
			writeFreshCloudRequirement(t, fixture.cloudCachePath())

			resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.NoError(t, err)
			assert.Equal(t, fixture.codexHome, resolution.dir)
			require.Len(t, resolution.warnings, 1)
			assert.NotContains(t, resolution.warnings[0], "secret-marker")
		})
	}
}

func TestResolveSQLiteDirKeepsCloudWarningWhenManagedPreferencesWin(t *testing.T) {
	fixture := newMachineFixture(t)
	signInToCloudAccount(t, fixture.codexHome)
	writeCloudCache(t, fixture.cloudCachePath(), fixtureNow.Add(-time.Hour), requirementsBundle())
	writeFixtureFile(t, fixture.computerPlist,
		xmlManagedPreferencesPlist(managedPreferencesRequirementsKey, `sqlite_home = "/Users/test/sqlite/managed"`))

	resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/sqlite/managed", resolution.dir)
	assert.Len(t, resolution.warnings, 1)
}

func TestResolveSQLiteDirRejectsCloudCacheWithABadSignature(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(t *testing.T, cache []byte) []byte
	}{
		{
			name: "a signed fragment was edited",
			tamper: func(_ *testing.T, cache []byte) []byte {
				return bytes.Replace(cache, []byte("/Users/test/sqlite/cloud"), []byte("/Users/test/sqlite/edited"), 1)
			},
		},
		{
			name: "the signature is not base64",
			tamper: func(t *testing.T, cache []byte) []byte {
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(cache, &fields))
				fields["signature"] = json.RawMessage(`"not base64!"`)
				tampered, err := json.Marshal(fields)
				require.NoError(t, err)
				return tampered
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			signInToCloudAccount(t, fixture.codexHome)
			writeCloudCache(t, fixture.cloudCachePath(), fixtureNow.Add(time.Hour), requirementsBundle(
				fixtureFragment{ID: "storage-policy", Name: "Storage policy", Contents: `sqlite_home = "/Users/test/sqlite/cloud"`}))
			cache, err := os.ReadFile(fixture.cloudCachePath())
			require.NoError(t, err)
			writeFixtureFile(t, fixture.cloudCachePath(), testCase.tamper(t, cache))

			_, err = resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.Error(t, err)
			assert.Contains(t, err.Error(), fixture.cloudCachePath())
		})
	}
}

func TestResolveSQLiteDirRejectsUnparsableMachineSources(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, fixture machineFixture)
	}{
		{
			name: "system requirements file is not TOML",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.systemRequirementsFile(), []byte("sqlite_home = "))
			},
		},
		{
			name: "system requirements sqlite_home is not a string",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.systemRequirementsFile(), []byte("sqlite_home = 5"))
			},
		},
		{
			name: "system config.toml is not TOML",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.systemConfigFile(), []byte("sqlite_home = "))
			},
		},
		{
			name: "managed_config.toml is not TOML even though a requirement wins",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.managedConfigFile(), []byte("sqlite_home = "))
				writeFixtureFile(t, fixture.systemRequirementsFile(), []byte(`sqlite_home = "/Users/test/sqlite/required"`))
			},
		},
		{
			name: "cloud cache is not JSON",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.cloudCachePath(), []byte("{"))
			},
		},
		{
			name: "cloud cache lacks its signature",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.cloudCachePath(), []byte(
					`{"signed_payload":{"version":1,"cached_at":"2026-09-01T11:00:00Z","expires_at":"2099-01-01T00:00:00Z",`+
						`"bundle":{"config_toml":{"enterprise_managed":[]},"requirements_toml":{"enterprise_managed":[]}}}}`,
				))
			},
		},
		{
			name: "cloud config fragments are not an array",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.cloudCachePath(), []byte(
					`{"signature":"","signed_payload":{"version":1,"cached_at":"2026-09-01T11:00:00Z","expires_at":"2099-01-01T00:00:00Z",`+
						`"bundle":{"config_toml":{"enterprise_managed":"not-a-list"},"requirements_toml":{"enterprise_managed":[]}}}}`,
				))
			},
		},
		{
			name: "a cloud fragment after the winning one is not TOML",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeCloudCache(t, fixture.cloudCachePath(), fixtureNow.Add(time.Hour), requirementsBundle(
					fixtureFragment{ID: "storage-policy", Name: "Storage policy", Contents: `sqlite_home = "/Users/test/sqlite/cloud"`},
					fixtureFragment{ID: "broken-policy", Name: "Broken policy", Contents: "sqlite_home = "}))
			},
		},
		{
			name: "a cloud config fragment is not TOML",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeCloudCache(t, fixture.cloudCachePath(), fixtureNow.Add(time.Hour), configBundle(
					fixtureFragment{ID: "broken-config", Name: "Broken config", Contents: "sqlite_home = "}))
			},
		},
		{
			name: "managed preferences plist is malformed",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.computerPlist, []byte("<plist><dict><key>requirements_toml_base64</key>"))
			},
		},
		{
			name: "managed preferences plist is truncated after the key",
			arrange: func(t *testing.T, fixture machineFixture) {
				complete := string(xmlManagedPreferencesPlist(managedPreferencesRequirementsKey, `sqlite_home = "/Users/test/sqlite/managed"`))
				truncated, _, cut := strings.Cut(complete, "</dict>")
				require.True(t, cut)
				writeFixtureFile(t, fixture.computerPlist, []byte(truncated))
			},
		},
		{
			name: "managed preferences value is not base64",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.computerPlist, binaryPlistFixture(t,
					plistEntry{key: managedPreferencesRequirementsKey, value: "not base64!"}))
			},
		},
		{
			name: "managed preferences config value is not a string",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.computerPlist, binaryPlistFixture(t,
					plistEntry{key: managedPreferencesConfigKey, value: 1}))
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			signInToCloudAccount(t, fixture.codexHome)
			testCase.arrange(t, fixture)

			_, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.Error(t, err)
		})
	}
}

func TestOpenRejectsExplicitSQLiteHomeThatDoesNotExist(t *testing.T) {
	cases := []struct {
		name string
		// arrange points one explicit source at missing and returns any
		// environment variables that source needs.
		arrange    func(t *testing.T, codexHome, missing string) map[string]string
		wantSource func(codexHome string) string
	}{
		{
			name: "config.toml",
			arrange: func(t *testing.T, codexHome, missing string) map[string]string {
				writeFixtureFile(t, filepath.Join(codexHome, configTOMLFileName), []byte(`sqlite_home = "`+missing+`"`))
				return nil
			},
			wantSource: func(codexHome string) string { return filepath.Join(codexHome, configTOMLFileName) },
		},
		{
			name: "environment",
			arrange: func(_ *testing.T, _, missing string) map[string]string {
				return map[string]string{sqliteHomeEnv: missing}
			},
			wantSource: func(string) string { return "$" + sqliteHomeEnv },
		},
		{
			name: "system requirement",
			arrange: func(t *testing.T, _, missing string) map[string]string {
				systemDir := t.TempDir()
				writeFixtureFile(t, filepath.Join(systemDir, systemRequirementsFileName), []byte(`sqlite_home = "`+missing+`"`))
				original := systemConfigDir
				systemConfigDir = systemDir
				t.Cleanup(func() { systemConfigDir = original })
				return nil
			},
			wantSource: func(string) string { return "managed requirements file" },
		},
		{
			name: "system config.toml",
			arrange: func(t *testing.T, _, missing string) map[string]string {
				systemDir := t.TempDir()
				writeFixtureFile(t, filepath.Join(systemDir, configTOMLFileName), []byte(`sqlite_home = "`+missing+`"`))
				original := systemConfigDir
				systemConfigDir = systemDir
				t.Cleanup(func() { systemConfigDir = original })
				return nil
			},
			wantSource: func(string) string { return "system config file" },
		},
		{
			name: "managed_config.toml",
			arrange: func(t *testing.T, _, missing string) map[string]string {
				systemDir := t.TempDir()
				writeFixtureFile(t, filepath.Join(systemDir, managedConfigFileName), []byte(`sqlite_home = "`+missing+`"`))
				original := systemConfigDir
				systemConfigDir = systemDir
				t.Cleanup(func() { systemConfigDir = original })
				return nil
			},
			wantSource: func(string) string { return "managed config file" },
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			codexHome, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			missing := filepath.Join(t.TempDir(), "missing-sqlite")
			environment := map[string]string{"HOME": t.TempDir()}
			maps.Copy(environment, testCase.arrange(t, codexHome, missing))
			adapter := NewAdapter(fakeGetenv(environment), noProcesses)

			_, err = adapter.Open(codexHome)

			require.ErrorIs(t, err, fs.ErrNotExist)
			assert.Contains(t, err.Error(), missing)
			assert.Contains(t, err.Error(), testCase.wantSource(codexHome))
		})
	}
}

func TestOpenRejectsExplicitSQLiteHomeThatIsAFile(t *testing.T) {
	codexHome := t.TempDir()
	sqliteFile := filepath.Join(t.TempDir(), "sqlite-file")
	writeFixtureFile(t, sqliteFile, []byte("not a directory"))
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": t.TempDir(), sqliteHomeEnv: sqliteFile}), noProcesses)

	_, err := adapter.Open(codexHome)

	require.Error(t, err)
}

func TestOpenAcceptsExplicitSQLiteHomeThatExists(t *testing.T) {
	codexHome := t.TempDir()
	sqliteHome := t.TempDir()
	writeFixtureFile(t, filepath.Join(codexHome, configTOMLFileName), []byte(`sqlite_home = "`+sqliteHome+`"`))
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": t.TempDir()}), noProcesses)

	opened, err := adapter.Open(codexHome)

	require.NoError(t, err)
	workspace, ok := opened.(*Workspace)
	require.True(t, ok)
	assert.Equal(t, sqliteHome, workspace.home.SQLiteDir)
}

func TestDefaultSQLiteDirThatDoesNotExistReadsAsNoDatabases(t *testing.T) {
	codexHome := t.TempDir()
	workspace := quietTestWorkspace(&Home{Dir: codexHome, SQLiteDir: filepath.Join(codexHome, "never-created")})

	databases, err := workspace.allDatabasePaths()

	require.NoError(t, err)
	assert.Empty(t, databases)
}

// openFixtureWithExpiredCloudCache opens the staged fixture home after signing
// it in to an account that gets cloud config and adding an expired cloud
// cache, so resolution records the cloud-cache warning.
func openFixtureWithExpiredCloudCache(t *testing.T) *Workspace {
	t.Helper()
	fixtureHome := SetupFixture(t)
	signInToCloudAccount(t, fixtureHome.Dir)
	expired := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	writeCloudCache(t, filepath.Join(fixtureHome.Dir, cloudConfigBundleCacheFile), expired, requirementsBundle())
	home, err := newHome(fixtureHome.Dir, fakeGetenv(nil))
	require.NoError(t, err)
	return quietTestWorkspace(home)
}

func TestResidualWarningsReportUncheckedCloudRequirement(t *testing.T) {
	workspace := openFixtureWithExpiredCloudCache(t)

	warnings, err := workspace.ResidualWarnings(tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/test/Projects/renamed"})

	require.NoError(t, err)
	assert.Contains(t, fmt.Sprint(warnings), "cloud-managed sqlite_home could not be checked")
}

func TestFinalizeReportsUncheckedCloudRequirement(t *testing.T) {
	workspace := openFixtureWithExpiredCloudCache(t)

	warnings, err := workspace.Finalize(context.Background(), FixtureProjectPath(), nil)

	require.NoError(t, err)
	assert.Contains(t, fmt.Sprint(warnings), "cloud-managed sqlite_home could not be checked")
}

func TestManagedSQLiteHomeSuppressesProfileOverlayDivergence(t *testing.T) {
	cases := []struct {
		name string
		tier sqliteHomeTier
	}{
		{name: "requirement", tier: sqliteHomeFromRequirement},
		{name: "managed config", tier: sqliteHomeFromManagedConfig},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			codexHome := t.TempDir()
			writeFixtureFile(t, filepath.Join(codexHome, "work.config.toml"), []byte(`sqlite_home = "/Users/test/sqlite/profile"`))
			workspace := quietTestWorkspace(&Home{
				Dir:          codexHome,
				SQLiteDir:    "/Users/test/sqlite/managed",
				sqliteSource: sqliteHomeSource{tier: testCase.tier, name: "managed source"},
			})

			err := workspace.projectAbsenceError()

			assert.ErrorIs(t, err, tool.ErrProjectAbsent)
		})
	}
}

func TestResolveSQLiteDirResolvesEmptyMachineValueToItsBaseDirectory(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, fixture machineFixture)
		wantDir func(fixture machineFixture) string
	}{
		{
			name: "system requirements file",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.systemRequirementsFile(), []byte(`sqlite_home = ""`))
			},
			wantDir: func(fixture machineFixture) string { return fixture.systemDir },
		},
		{
			name: "managed-preference config",
			arrange: func(t *testing.T, fixture machineFixture) {
				writeFixtureFile(t, fixture.computerPlist, xmlManagedPreferencesPlist(managedPreferencesConfigKey, `sqlite_home = ""`))
			},
			wantDir: func(fixture machineFixture) string { return fixture.codexHome },
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			testCase.arrange(t, fixture)

			resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.NoError(t, err)
			assert.Equal(t, testCase.wantDir(fixture), resolution.dir)
		})
	}
}

func TestResolveSQLiteDirWarnsWhenCodexCannotLoadTheChatGPTTokens(t *testing.T) {
	idToken := syntheticIDToken(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "enterprise", "chatgpt_user_id": "fixture-user"},
	})
	cases := []struct {
		name   string
		tokens map[string]any
	}{
		{
			name:   "access and refresh tokens missing",
			tokens: map[string]any{"id_token": idToken, "account_id": "fixture-account"},
		},
		{
			name: "id_token null",
			tokens: map[string]any{
				"id_token": nil, "access_token": "fixture-access", "refresh_token": "fixture-refresh", "account_id": "fixture-account",
			},
		},
		{
			name: "account_id not a string",
			tokens: map[string]any{
				"id_token": idToken, "access_token": "fixture-access", "refresh_token": "fixture-refresh", "account_id": 7,
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			auth, err := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": testCase.tokens})
			require.NoError(t, err)
			writeFixtureFile(t, filepath.Join(fixture.codexHome, codexAuthFile), auth)
			writeFreshCloudRequirement(t, fixture.cloudCachePath())

			resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.NoError(t, err)
			assert.Equal(t, fixture.codexHome, resolution.dir, "a token object Codex cannot load must never supply a requirement")
			require.Len(t, resolution.warnings, 1)
			assert.Contains(t, resolution.warnings[0], "cloud-managed sqlite_home could not be checked")
		})
	}
}

func TestResolveSQLiteDirRefusesMalformedManagedPreferencesWithoutEchoingThem(t *testing.T) {
	cases := []struct {
		name  string
		plist string
	}{
		{name: "unexpected text", plist: `<plist><dict>secret-marker</dict></plist>`},
		{name: "wrong root element", plist: `<secret-marker><dict></dict></secret-marker>`},
		{name: "mismatched element", plist: `<plist><dict><key>requirements_toml_base64</key><string>x</secret-marker></dict></plist>`},
		{name: "text after the document", plist: `<plist><dict></dict></plist>secret-marker`},
		{
			name:  "invalid TOML payload",
			plist: string(xmlManagedPreferencesPlist(managedPreferencesRequirementsKey, `sqlite_home = secret-marker`)),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			writeFixtureFile(t, fixture.computerPlist, []byte(testCase.plist))

			_, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret-marker")
		})
	}
}

func TestProjectAbsenceErrorIsUnresolvedWhenTheCloudCacheCouldNotBeChecked(t *testing.T) {
	workspace := openFixtureWithExpiredCloudCache(t)

	err := workspace.projectAbsenceError()

	require.ErrorIs(t, err, ErrProjectAbsenceUnresolved)
	require.NotErrorIs(t, err, tool.ErrProjectAbsent)
	assert.Contains(t, err.Error(), "cloud-managed sqlite_home could not be checked")
}

// agentIdentityAuth is an agent-identity auth.json holding a stored record;
// taskID "" leaves task_id out, as a record still needing registration does.
func agentIdentityAuth(t *testing.T, plan, accountID, taskID string) []byte {
	t.Helper()
	record := map[string]any{
		"agent_runtime_id": "fixture-runtime", "agent_private_key": "fixture-key", "account_id": accountID,
		"chatgpt_user_id": "fixture-user", "email": nil, "plan_type": plan, "chatgpt_account_is_fedramp": false,
	}
	if taskID != "" {
		record["task_id"] = taskID
	}
	auth, err := json.Marshal(map[string]any{"auth_mode": "agentIdentity", "agent_identity": record})
	require.NoError(t, err)
	return auth
}

func TestResolveSQLiteDirGatesAgentIdentityAuth(t *testing.T) {
	cases := []struct {
		name         string
		auth         func(t *testing.T) []byte
		wantDir      func(codexHome string) string
		wantWarnings int
	}{
		{
			name: "a registered record on an eligible plan applies the matching cache",
			auth: func(t *testing.T) []byte {
				return agentIdentityAuth(t, "enterprise", "fixture-account", "fixture-task")
			},
			wantDir: func(string) string { return "/Users/test/sqlite/cloud" },
		},
		{
			name:         "a registered record for another account is skipped with a warning",
			auth:         func(t *testing.T) []byte { return agentIdentityAuth(t, "enterprise", "other-account", "fixture-task") },
			wantDir:      func(codexHome string) string { return codexHome },
			wantWarnings: 1,
		},
		{
			name:    "a registered record on an ineligible plan never reads the cache",
			auth:    func(t *testing.T) []byte { return agentIdentityAuth(t, "plus", "fixture-account", "fixture-task") },
			wantDir: func(codexHome string) string { return codexHome },
		},
		{
			name:         "a record without a task needs the network and warns",
			auth:         func(t *testing.T) []byte { return agentIdentityAuth(t, "enterprise", "fixture-account", "") },
			wantDir:      func(codexHome string) string { return codexHome },
			wantWarnings: 1,
		},
		{
			name: "a JWT needs the network and warns",
			auth: func(*testing.T) []byte {
				return []byte(`{"auth_mode":"agentIdentity","agent_identity":"header.payload.signature"}`)
			},
			wantDir:      func(codexHome string) string { return codexHome },
			wantWarnings: 1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			writeFixtureFile(t, filepath.Join(fixture.codexHome, codexAuthFile), testCase.auth(t))
			writeFreshCloudRequirement(t, fixture.cloudCachePath())

			resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.NoError(t, err)
			assert.Equal(t, testCase.wantDir(fixture.codexHome), resolution.dir)
			assert.Len(t, resolution.warnings, testCase.wantWarnings)
		})
	}
}

func TestResolveSQLiteDirRejectsARepeatedDeclaredCacheField(t *testing.T) {
	cases := []struct {
		name         string
		from, repeat string
	}{
		{name: "in signed_payload", from: `"version": 1,`, repeat: `"version": 1, "version": 1,`},
		{name: "in a fragment", from: `"id": "storage-policy",`, repeat: `"id": "storage-policy", "id": "storage-policy",`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMachineFixture(t)
			signInToCloudAccount(t, fixture.codexHome)
			writeFreshCloudRequirement(t, fixture.cloudCachePath())
			cache, err := os.ReadFile(fixture.cloudCachePath())
			require.NoError(t, err)
			repeated := bytes.Replace(cache, []byte(testCase.from), []byte(testCase.repeat), 1)
			require.NotEqual(t, cache, repeated)
			writeFixtureFile(t, fixture.cloudCachePath(), repeated)

			_, err = resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

			require.Error(t, err)
			assert.Contains(t, err.Error(), fixture.cloudCachePath())
			assert.Contains(t, err.Error(), "repeated field at byte")
		})
	}
}

func TestResolveSQLiteDirIgnoresARepeatedUnknownCacheField(t *testing.T) {
	fixture := newMachineFixture(t)
	signInToCloudAccount(t, fixture.codexHome)
	writeFreshCloudRequirement(t, fixture.cloudCachePath())
	cache, err := os.ReadFile(fixture.cloudCachePath())
	require.NoError(t, err)
	repeated := bytes.Replace(cache, []byte(`"id": "storage-policy",`),
		[]byte(`"id": "storage-policy", "unknown_field": 1, "unknown_field": 2,`), 1)
	require.NotEqual(t, cache, repeated)
	writeFixtureFile(t, fixture.cloudCachePath(), repeated)

	resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/sqlite/cloud", resolution.dir)
}

func TestResolveSQLiteDirIgnoresARepeatedUnknownAuthField(t *testing.T) {
	fixture := newMachineFixture(t)
	signInToCloudAccount(t, fixture.codexHome)
	auth, err := os.ReadFile(filepath.Join(fixture.codexHome, codexAuthFile))
	require.NoError(t, err)
	repeated := append(bytes.TrimSuffix(auth, []byte("}")), []byte(`,"unknown_field":1,"unknown_field":2}`)...)
	writeFixtureFile(t, filepath.Join(fixture.codexHome, codexAuthFile), repeated)
	writeFreshCloudRequirement(t, fixture.cloudCachePath())

	resolution, err := resolveSQLiteDir(fixture.codexHome, fakeGetenv(nil), fixture.sources(), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/sqlite/cloud", resolution.dir)
}
