package codex

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

// Codex's machine-level configuration locations outside the codex home. They
// are process-wide and not configurable, so tests redirect these variables
// away from the machine's real /etc and /Library.
var (
	// systemConfigDir holds Codex's Unix system requirements.toml
	// (config/src/loader/mod.rs, system_requirements_toml_file), config.toml
	// (config/src/loader/mod.rs:75), and managed_config.toml
	// (config/src/loader/layer_io.rs:22).
	systemConfigDir = "/etc/codex"
	// managedPreferencesDir holds the forced managed-preference plists that
	// back Codex's MDM layers. Codex reads them only on macOS
	// (config/src/loader/managed_requirements.rs:79-93,
	// config/src/loader/layer_io.rs, load_config_layers_internal), so it is
	// empty elsewhere.
	managedPreferencesDir = defaultManagedPreferencesDir()
)

func defaultManagedPreferencesDir() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Managed Preferences"
	}
	return ""
}

const (
	systemRequirementsFileName = "requirements.toml"
	managedConfigFileName      = "managed_config.toml"

	// managedPreferencesDomain names the forced preferences Codex reads, and
	// the two keys carry base64-encoded config and requirements TOML
	// (config/src/loader/macos.rs:27-29).
	managedPreferencesDomain          = "com.openai.codex"
	managedPreferencesConfigKey       = "config_toml_base64"
	managedPreferencesRequirementsKey = "requirements_toml_base64"

	// cloudConfigBundleCacheFile is the cloud config bundle cache under the
	// codex home (cloud-config/src/cache.rs:24).
	cloudConfigBundleCacheFile = "cloud-config-bundle-cache.json"
	// cloudConfigBundleCacheVersion is the only cache layout Codex accepts
	// (cloud-config/src/cache.rs:23, 85-89).
	cloudConfigBundleCacheVersion = 1

	codexAuthFile = "auth.json"
)

// cloudConfigBundleCacheHMACKey is the key Codex signs and verifies its cloud
// config bundle cache with (cloud-config/src/cache.rs:26-29).
var cloudConfigBundleCacheHMACKey = []byte("codex-cloud-config-bundle-cache-v1-6160ae70-bcfd-4ca8-a99b-40f73b3b072e")

// managedSources names the machine-level files sqlite_home can come from.
// managedPreferencePlists is ordered highest precedence first.
type managedSources struct {
	systemRequirementsFile  string
	systemConfigFile        string
	managedConfigFile       string
	managedPreferencePlists []string
	cloudCacheFile          string
	authFile                string
}

// machineManagedSources returns the files Codex would read for the codex home
// dir.
func machineManagedSources(dir string) (managedSources, error) {
	sources := managedSources{
		systemRequirementsFile: filepath.Join(systemConfigDir, systemRequirementsFileName),
		systemConfigFile:       filepath.Join(systemConfigDir, configTOMLFileName),
		managedConfigFile:      filepath.Join(systemConfigDir, managedConfigFileName),
		cloudCacheFile:         filepath.Join(dir, cloudConfigBundleCacheFile),
		authFile:               filepath.Join(dir, codexAuthFile),
	}
	if managedPreferencesDir == "" {
		return sources, nil
	}
	current, err := user.Current()
	if err != nil {
		return managedSources{}, fmt.Errorf("determine the user for managed preferences: %w", err)
	}
	plistName := managedPreferencesDomain + ".plist"
	// A user-level forced preference shadows the computer-level one.
	sources.managedPreferencePlists = []string{
		filepath.Join(managedPreferencesDir, current.Username, plistName),
		filepath.Join(managedPreferencesDir, plistName),
	}
	return sources, nil
}

// sqliteHomeSetting is one source's sqlite_home, resolved to an absolute path.
type sqliteHomeSetting struct {
	path   string
	source string
}

// managedSQLiteHome is what the machine-level sources say about sqlite_home,
// grouped by where each group sits relative to config.toml, plus the warnings
// for sources that exist but could not be checked. A nil group means no
// source in it sets sqlite_home.
type managedSQLiteHome struct {
	// requirement replaces every configured value
	// (core/src/config/requirements.rs:35, 76-101).
	requirement *sqliteHomeSetting
	// managedConfig comes from config layers that outrank config.toml,
	// profiles, and project config: managed_config.toml, then the MDM
	// config_toml_base64 preference (config/src/config_layer_source.rs:33-51).
	managedConfig *sqliteHomeSetting
	// machineConfig comes from config layers config.toml outranks: the system
	// config.toml, then cloud config fragments.
	machineConfig *sqliteHomeSetting
	warnings      []string
}

// resolveManagedSQLiteHome reads every machine-level source. Requirements
// compose lowest precedence first as system file, cloud bundle, MDM
// (config/src/loader/managed_requirements.rs:104-113); config layers as
// system config.toml, cloud fragments, config.toml, profile, project,
// managed_config.toml, MDM (config/src/config_layer_source.rs:33-51). In each
// group the highest source setting sqlite_home wins. Every source is read even
// when a higher one wins, because Codex fails to load when any layer does not
// parse.
//
// A relative value resolves against the directory of the file that holds it,
// or against codexHome for the cloud and MDM sources
// (config/src/loader/managed_requirements.rs:42-43, 81-88;
// config/src/loader/mod.rs:208, 434-445, 457; load_requirements_toml,
// load_config_toml_for_required_layer). osHome expands a leading ~.
func resolveManagedSQLiteHome(codexHome string, sources *managedSources, osHome string, now time.Time) (managedSQLiteHome, error) {
	var resolved managedSQLiteHome
	auth, err := readCloudAuth(sources.authFile)
	if err != nil {
		return managedSQLiteHome{}, err
	}
	cloud, cloudWarning, err := readCloudConfigBundle(sources.cloudCacheFile, auth, now)
	if err != nil {
		return managedSQLiteHome{}, err
	}
	if cloudWarning != "" {
		resolved.warnings = append(resolved.warnings, cloudWarning)
	}

	systemRequirement, err := sqliteHomeFromFile(sources.systemRequirementsFile, "managed requirements file", osHome)
	if err != nil {
		return managedSQLiteHome{}, err
	}
	cloudRequirement, err := sqliteHomeFromFragments(cloud.requirementFragments, sources.cloudCacheFile, "requirements", codexHome, osHome)
	if err != nil {
		return managedSQLiteHome{}, err
	}
	mdmRequirement, err := sqliteHomeFromManagedPreference(sources.managedPreferencePlists, managedPreferencesRequirementsKey, codexHome, osHome)
	if err != nil {
		return managedSQLiteHome{}, err
	}
	systemConfig, err := sqliteHomeFromFile(sources.systemConfigFile, "system config file", osHome)
	if err != nil {
		return managedSQLiteHome{}, err
	}
	cloudConfig, err := sqliteHomeFromFragments(cloud.configFragments, sources.cloudCacheFile, "config", codexHome, osHome)
	if err != nil {
		return managedSQLiteHome{}, err
	}
	managedConfig, err := sqliteHomeFromFile(sources.managedConfigFile, "managed config file", osHome)
	if err != nil {
		return managedSQLiteHome{}, err
	}
	mdmConfig, err := sqliteHomeFromManagedPreference(sources.managedPreferencePlists, managedPreferencesConfigKey, codexHome, osHome)
	if err != nil {
		return managedSQLiteHome{}, err
	}

	resolved.requirement = highestSetting(systemRequirement, cloudRequirement, mdmRequirement)
	resolved.machineConfig = highestSetting(systemConfig, cloudConfig)
	resolved.managedConfig = highestSetting(managedConfig, mdmConfig)
	return resolved, nil
}

// highestSetting returns the last non-nil setting; callers list settings
// lowest precedence first.
func highestSetting(settings ...*sqliteHomeSetting) *sqliteHomeSetting {
	var highest *sqliteHomeSetting
	for _, setting := range settings {
		if setting != nil {
			highest = setting
		}
	}
	return highest
}

// sqliteHomeFromFile reads one system-directory file. A relative value
// resolves against the file's own directory.
func sqliteHomeFromFile(path, kind, osHome string) (*sqliteHomeSetting, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: Codex's fixed system configuration paths
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s %s: %w", kind, path, err)
	}
	return sqliteHomeFromTOML(data, kind+" "+path, filepath.Dir(path), osHome)
}

// sqliteHomeValue reads the sqlite_home key. present distinguishes an empty
// value, which Codex resolves to the layer's base directory, from an absent
// key. Errors carry the position only, never document text, because the
// document may be a managed-preference or cloud payload.
func sqliteHomeValue(data []byte) (value string, present bool, err error) {
	var probe struct {
		SQLiteHome *string `toml:"sqlite_home"`
	}
	if err := toml.Unmarshal(data, &probe); err != nil {
		var decodeErr *toml.DecodeError
		if errors.As(err, &decodeErr) {
			row, column := decodeErr.Position()
			return "", false, fmt.Errorf("invalid TOML or sqlite_home type at line %d, column %d", row, column)
		}
		return "", false, errors.New("invalid TOML or sqlite_home type")
	}
	if probe.SQLiteHome == nil {
		return "", false, nil
	}
	return *probe.SQLiteHome, true, nil
}

func sqliteHomeFromTOML(data []byte, source, base, osHome string) (*sqliteHomeSetting, error) {
	value, present, err := sqliteHomeValue(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s for sqlite_home: %w", source, err)
	}
	if !present {
		return nil, nil
	}
	path, err := resolveAgainstHome(base, value, osHome)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite_home from %s: %w", source, err)
	}
	return &sqliteHomeSetting{path: path, source: source}, nil
}

// sqliteHomeFromFragments parses every fragment of one cloud bundle bucket.
// Fragments arrive highest precedence first (config/src/cloud_config_bundle.rs,
// CloudRequirementsTomlBundle::into_layers; config/src/cloud_config_layers.rs:117-119),
// so the first one declaring sqlite_home wins. Every fragment is still parsed:
// Codex rejects the whole bundle when one does not parse.
func sqliteHomeFromFragments(fragments []cloudFragment, cachePath, bucket, codexHome, osHome string) (*sqliteHomeSetting, error) {
	var winner *sqliteHomeSetting
	for index, fragment := range fragments {
		// The position, not the fragment's name: errors never carry text
		// from a managed payload.
		source := fmt.Sprintf("cloud config bundle cache %s (%s fragment %d)", cachePath, bucket, index+1)
		setting, err := sqliteHomeFromTOML([]byte(fragment.contents), source, codexHome, osHome)
		if err != nil {
			return nil, err
		}
		if winner == nil {
			winner = setting
		}
	}
	return winner, nil
}

// sqliteHomeFromManagedPreference reads one forced preference key from the
// plist files that back macOS forced preferences. Codex asks CFPreferences for
// the value instead (config/src/loader/macos.rs, load_managed_preference);
// these files are the on-disk store behind that API, not a path upstream
// names. The first plist carrying the key supplies the value, so a lower plist
// is never consulted for that key once a higher one sets it.
func sqliteHomeFromManagedPreference(plists []string, key, codexHome, osHome string) (*sqliteHomeSetting, error) {
	for _, path := range plists {
		data, err := os.ReadFile(path) //nolint:gosec // G304: fixed managed-preferences location
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read managed preferences %s: %w", path, err)
		}
		encoded, found, err := plistTopLevelString(data, key)
		if err != nil {
			return nil, fmt.Errorf("parse managed preferences %s: %w", path, err)
		}
		if !found {
			continue
		}
		// Codex trims the value and decodes it as standard base64 into UTF-8
		// TOML (config/src/loader/macos.rs, decode_managed_preferences_base64).
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, fmt.Errorf("decode %s in %s: %w", key, path, err)
		}
		if !utf8.Valid(decoded) {
			return nil, fmt.Errorf("decode %s in %s: contents are not valid UTF-8", key, path)
		}
		source := fmt.Sprintf("managed preferences %s (%s %s)", path, managedPreferencesDomain, key)
		return sqliteHomeFromTOML(decoded, source, codexHome, osHome)
	}
	return nil, nil
}

// cloudFragment is one decoded fragment of a verified cloud bundle.
type cloudFragment struct {
	contents string
}

// cloudConfigBundle is the part of a verified, unexpired cache cc-port uses.
// Its zero value stands for "no bundle".
type cloudConfigBundle struct {
	configFragments      []cloudFragment
	requirementFragments []cloudFragment
}

// cloudConfigBundleCacheJSON mirrors Codex's CloudConfigBundleCacheFile
// (cloud-config/src/cache.rs:198-212, config/src/cloud_config_bundle.rs:24-55,
// 80-85, config/src/cloud_config_layers.rs:27-31). Every value stays a raw
// JSON token: Codex signs serde_json's compact serialization of the payload,
// and a Codex-written cache holds exactly those tokens, so writing them back
// in field order reproduces the signed bytes.
type cloudConfigBundleCacheJSON struct {
	Signature     json.RawMessage `json:"signature"`
	SignedPayload *struct {
		Version       json.RawMessage `json:"version"`
		CachedAt      json.RawMessage `json:"cached_at"`
		ExpiresAt     json.RawMessage `json:"expires_at"`
		ChatGPTUserID json.RawMessage `json:"chatgpt_user_id"`
		AccountID     json.RawMessage `json:"account_id"`
		Bundle        *struct {
			ConfigTOML       *cloudBundleBucketJSON `json:"config_toml"`
			RequirementsTOML *cloudBundleBucketJSON `json:"requirements_toml"`
		} `json:"bundle"`
	} `json:"signed_payload"`
}

type cloudBundleBucketJSON struct {
	EnterpriseManaged *[]cloudBundleFragmentJSON `json:"enterprise_managed"`
}

type cloudBundleFragmentJSON struct {
	ID       json.RawMessage `json:"id"`
	Name     json.RawMessage `json:"name"`
	Contents json.RawMessage `json:"contents"`
}

// readCloudConfigBundle returns the cloud config bundle Codex would apply,
// following Codex's startup order (cloud-config/src/service.rs:183-207):
// auth gate, then CloudConfigBundleCache::load (cloud-config/src/cache.rs:49-107),
// which checks the account identity, parses, verifies the signature, checks
// the version, matches the cached identity, then the expiry. Agent-identity
// and personal-access-token auth need the network to decide, so they yield a
// warning and no bundle. A cache that
// fails parsing, the signature, or the version check is an error. When Codex
// would skip the cache for an account it applies cloud config to (missing,
// identity incomplete or mismatched, expired), it fetches the bundle from the
// network instead, which cc-port cannot; that yields a warning and no bundle.
func readCloudConfigBundle(cachePath string, auth cloudAuth, now time.Time) (cloudConfigBundle, string, error) {
	switch auth.gate {
	case cloudConfigNeverApplies:
		return cloudConfigBundle{}, "", nil
	case cloudConfigUndecidable:
		return cloudConfigBundle{}, cloudCacheWarning(auth.undecidableReason), nil
	case cloudConfigAppliesToAccount:
		// Checked against the cache below.
	}
	if auth.chatgptUserID == nil || auth.accountID == nil {
		return cloudConfigBundle{}, cloudCacheWarning(
			"auth.json carries no complete account identity, so Codex fetches the bundle without reading " + cachePath,
		), nil
	}
	data, err := os.ReadFile(cachePath) //nolint:gosec // G304: path constructed from the resolved codex home
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return cloudConfigBundle{}, "", fmt.Errorf("read cloud config bundle cache %s: %w", cachePath, err)
		}
		return cloudConfigBundle{}, cloudCacheRefreshWarning(cachePath + " does not exist, so Codex fetches the bundle for this account"), nil
	}

	offset, err := repeatedDeclaredField(data, cloudCacheShape, 0)
	if err != nil {
		return cloudConfigBundle{}, "", fmt.Errorf("parse cloud config bundle cache %s: %w", cachePath, err)
	}
	if offset >= 0 {
		return cloudConfigBundle{}, "", fmt.Errorf("parse cloud config bundle cache %s: repeated field at byte %d", cachePath, offset)
	}
	var cache cloudConfigBundleCacheJSON
	if err := json.Unmarshal(data, &cache); err != nil {
		return cloudConfigBundle{}, "", fmt.Errorf("parse cloud config bundle cache %s: %w", cachePath, jsonReason(err))
	}
	decoded, err := decodeCloudConfigBundleCache(cache)
	if err != nil {
		return cloudConfigBundle{}, "", fmt.Errorf("parse cloud config bundle cache %s: %w", cachePath, err)
	}
	if !hmac.Equal(cloudCacheSignature(cache), decoded.signature) {
		return cloudConfigBundle{}, "", fmt.Errorf("cloud config bundle cache %s failed signature verification", cachePath)
	}
	if decoded.version != cloudConfigBundleCacheVersion {
		return cloudConfigBundle{}, "", fmt.Errorf(
			"cloud config bundle cache %s has unsupported version %d (cc-port reads version %d)",
			cachePath, decoded.version, cloudConfigBundleCacheVersion,
		)
	}
	if !sameIdentity(&decoded, auth) {
		return cloudConfigBundle{}, cloudCacheRefreshWarning(cachePath + " was cached for a different account, so Codex refetches the bundle"), nil
	}
	if !decoded.expiresAt.After(now) {
		return cloudConfigBundle{}, cloudCacheRefreshWarning(fmt.Sprintf(
			"%s expired at %s, so Codex refetches the bundle", cachePath, decoded.expiresAt.UTC().Format(time.RFC3339),
		)), nil
	}
	return decoded.bundle, "", nil
}

// sameIdentity mirrors cache.rs:91-100: both cached identifiers must be
// present and equal to the signed-in account's.
func sameIdentity(decoded *decodedCloudCache, auth cloudAuth) bool {
	return decoded.chatgptUserID != nil && decoded.accountID != nil &&
		*decoded.chatgptUserID == *auth.chatgptUserID && *decoded.accountID == *auth.accountID
}

func cloudCacheWarning(reason string) string {
	return "cloud-managed sqlite_home could not be checked: " + reason + ", which cc-port cannot"
}

// cloudCacheRefreshWarning is cloudCacheWarning for a reason a Codex run
// clears by rewriting the cache: a missing, foreign, or expired file.
func cloudCacheRefreshWarning(reason string) string {
	return cloudCacheWarning(reason) + "; start Codex once to refresh the cache"
}

type decodedCloudCache struct {
	signature     []byte
	version       uint32
	expiresAt     time.Time
	chatgptUserID *string
	accountID     *string
	bundle        cloudConfigBundle
}

// decodeCloudConfigBundleCache checks every field Codex's deserializer
// requires and decodes the ones cc-port uses. chatgpt_user_id and account_id
// are Option<String> upstream, so they may be absent or null.
func decodeCloudConfigBundleCache(cache cloudConfigBundleCacheJSON) (decodedCloudCache, error) {
	var decoded decodedCloudCache
	var signature string
	if err := decodeRequiredJSON(cache.Signature, "signature", &signature); err != nil {
		return decodedCloudCache{}, err
	}
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		// Codex reads an undecodable signature as a failed verification
		// (cloud-config/src/cache.rs, verify_cache_signature).
		signatureBytes = nil
	}
	decoded.signature = signatureBytes

	payload := cache.SignedPayload
	if payload == nil {
		return decodedCloudCache{}, errors.New("missing signed_payload")
	}
	var cachedAt time.Time
	for _, field := range []struct {
		raw    json.RawMessage
		name   string
		target any
	}{
		{payload.Version, "signed_payload.version", &decoded.version},
		{payload.CachedAt, "signed_payload.cached_at", &cachedAt},
		{payload.ExpiresAt, "signed_payload.expires_at", &decoded.expiresAt},
	} {
		if err := decodeRequiredJSON(field.raw, field.name, field.target); err != nil {
			return decodedCloudCache{}, err
		}
	}
	for _, field := range []struct {
		raw    json.RawMessage
		name   string
		target **string
	}{
		{payload.ChatGPTUserID, "signed_payload.chatgpt_user_id", &decoded.chatgptUserID},
		{payload.AccountID, "signed_payload.account_id", &decoded.accountID},
	} {
		if field.raw != nil {
			if err := json.Unmarshal(field.raw, field.target); err != nil {
				return decodedCloudCache{}, fmt.Errorf("decode %s: %w", field.name, jsonReason(err))
			}
		}
	}
	if payload.Bundle == nil {
		return decodedCloudCache{}, errors.New("missing signed_payload.bundle")
	}
	decoded.bundle.configFragments, err = decodeCloudBucket(payload.Bundle.ConfigTOML, "config_toml")
	if err != nil {
		return decodedCloudCache{}, err
	}
	decoded.bundle.requirementFragments, err = decodeCloudBucket(payload.Bundle.RequirementsTOML, "requirements_toml")
	if err != nil {
		return decodedCloudCache{}, err
	}
	return decoded, nil
}

func decodeCloudBucket(bucket *cloudBundleBucketJSON, name string) ([]cloudFragment, error) {
	if bucket == nil || bucket.EnterpriseManaged == nil {
		return nil, fmt.Errorf("missing signed_payload.bundle.%s.enterprise_managed", name)
	}
	fragments := make([]cloudFragment, 0, len(*bucket.EnterpriseManaged))
	for index, raw := range *bucket.EnterpriseManaged {
		field := fmt.Sprintf("signed_payload.bundle.%s.enterprise_managed[%d]", name, index)
		var id, fragmentName string
		var fragment cloudFragment
		if err := decodeRequiredJSON(raw.ID, field+".id", &id); err != nil {
			return nil, err
		}
		if err := decodeRequiredJSON(raw.Name, field+".name", &fragmentName); err != nil {
			return nil, err
		}
		if err := decodeRequiredJSON(raw.Contents, field+".contents", &fragment.contents); err != nil {
			return nil, err
		}
		fragments = append(fragments, fragment)
	}
	return fragments, nil
}

// decodeRequiredJSON decodes a field serde requires: absent and null are both
// errors.
func decodeRequiredJSON(raw json.RawMessage, name string, target any) error {
	if raw == nil || bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("missing %s", name)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode %s: %w", name, jsonReason(err))
	}
	return nil
}

// jsonReason reduces an encoding/json error to a byte offset or a type
// mismatch, because a syntax error message quotes a character of the cache.
func jsonReason(err error) error {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("invalid JSON at byte %d", syntaxErr.Offset)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Errorf("a JSON %s where the cache layout needs another type", typeErr.Value)
	}
	return errors.New("invalid JSON")
}

// cloudCacheSignature computes the HMAC-SHA256 Codex expects over the signed
// payload (cloud-config/src/cache.rs, cache_payload_bytes and
// sign_cache_payload): serde_json's compact form of
// CloudConfigBundleCacheSignedPayload, fields in declaration order, an absent
// Option written as null. decodeCloudConfigBundleCache has already rejected
// every missing required field.
func cloudCacheSignature(cache cloudConfigBundleCacheJSON) []byte {
	payload := cache.SignedPayload
	var signed bytes.Buffer
	writeRaw := func(raw json.RawMessage) {
		if raw == nil {
			signed.WriteString("null")
			return
		}
		signed.Write(raw)
	}
	writeBucket := func(bucket *cloudBundleBucketJSON) {
		signed.WriteString(`{"enterprise_managed":[`)
		for index, fragment := range *bucket.EnterpriseManaged {
			if index > 0 {
				signed.WriteByte(',')
			}
			signed.WriteString(`{"id":`)
			writeRaw(fragment.ID)
			signed.WriteString(`,"name":`)
			writeRaw(fragment.Name)
			signed.WriteString(`,"contents":`)
			writeRaw(fragment.Contents)
			signed.WriteByte('}')
		}
		signed.WriteString(`]}`)
	}
	signed.WriteString(`{"version":`)
	writeRaw(payload.Version)
	signed.WriteString(`,"cached_at":`)
	writeRaw(payload.CachedAt)
	signed.WriteString(`,"expires_at":`)
	writeRaw(payload.ExpiresAt)
	signed.WriteString(`,"chatgpt_user_id":`)
	writeRaw(payload.ChatGPTUserID)
	signed.WriteString(`,"account_id":`)
	writeRaw(payload.AccountID)
	signed.WriteString(`,"bundle":{"config_toml":`)
	writeBucket(payload.Bundle.ConfigTOML)
	signed.WriteString(`,"requirements_toml":`)
	writeBucket(payload.Bundle.RequirementsTOML)
	signed.WriteString(`}}`)

	mac := hmac.New(sha256.New, cloudConfigBundleCacheHMACKey)
	mac.Write(signed.Bytes())
	return mac.Sum(nil)
}

// cloudAuthGate is what auth.json says about Codex applying cloud config.
type cloudAuthGate int

const (
	// cloudConfigNeverApplies: no auth.json, an auth mode that does not use
	// the Codex backend, or a plan cloud config is not offered for
	// (cloud-config/src/service.rs:50-58, 186-191).
	cloudConfigNeverApplies cloudAuthGate = iota
	// cloudConfigAppliesToAccount: ChatGPT auth, or agent-identity auth with
	// a registered task, on an eligible plan; the cache applies only to the
	// matching account.
	cloudConfigAppliesToAccount
	// cloudConfigUndecidable: auth whose plan and identity Codex resolves
	// over the network, or an auth.json Codex cannot load, so cc-port cannot
	// tell whether or which bundle applies.
	cloudConfigUndecidable
)

// cloudAuth is the gate plus, for cloudConfigAppliesToAccount, the two
// identifiers the cache is matched against. They are compared and dropped;
// no error or warning ever carries them.
type cloudAuth struct {
	gate cloudAuthGate
	// undecidableReason says why cloudConfigUndecidable was reached, in
	// fixed words that carry no auth.json value.
	undecidableReason string
	chatgptUserID     *string
	accountID         *string
}

func undecidableAuth(reason string) cloudAuth {
	return cloudAuth{gate: cloudConfigUndecidable, undecidableReason: reason}
}

// authCannotLoad is the reason for any auth.json Codex's deserializer or
// loader rejects: Codex then has no sign-in to fetch cloud config for, and
// cc-port does not guess what a repaired file would say.
const authCannotLoad = "auth.json does not match the layout Codex loads, so Codex cannot load this sign-in"

// authDotJSON is what parseAuthDotJSON keeps of Codex's AuthDotJson
// (login/src/auth/storage.rs:41-65) after checking every declared field's
// type the way serde would.
type authDotJSON struct {
	authMode               *string
	hasOpenAIAPIKey        bool
	hasPersonalAccessToken bool
	hasBedrockAPIKey       bool
	hasBedrockAccessKeys   bool
	tokens                 *tokenData
	agentIdentity          *agentIdentityStorage
}

// tokenData is what the gate needs from a TokenData
// (login/src/token_data.rs:11-25) whose required fields all have their type.
type tokenData struct {
	claims    idTokenClaims
	accountID *string
}

// agentIdentityStorage mirrors the untagged AgentIdentityStorage
// (login/src/auth/storage.rs:67-109): a JWT string, or a stored record.
type agentIdentityStorage struct {
	record *agentIdentityRecord
}

type agentIdentityRecord struct {
	accountID     string
	chatgptUserID string
	planType      string
	taskID        *string
}

// AuthMode's serde names (protocol/src/auth.rs:6-37).
const (
	authModeAPIKey              = "apikey"
	authModeChatGPT             = "chatgpt"
	authModeChatGPTAuthTokens   = "chatgptAuthTokens" //nolint:gosec // G101: an auth mode name, not a credential
	authModeHeaders             = "headers"
	authModeAgentIdentity       = "agentIdentity"
	authModePersonalAccessToken = "personalAccessToken"
	authModeBedrockAPIKey       = "bedrockApiKey"
	authModeBedrockAccessKeys   = "bedrockAccessKeys"
)

var authModes = map[string]bool{
	authModeAPIKey: true, authModeChatGPT: true, authModeChatGPTAuthTokens: true, authModeHeaders: true,
	authModeAgentIdentity: true, authModePersonalAccessToken: true, authModeBedrockAPIKey: true, authModeBedrockAccessKeys: true,
}

// Field names shared by several of Codex's auth structs.
const (
	authFieldAccountID     = "account_id"
	authFieldChatGPTUserID = "chatgpt_user_id"
	authFieldEmail         = "email"
)

// readCloudAuth derives the cloud gate from auth.json the way Codex's
// startup would: load it (login/src/auth/manager.rs:313-424), then apply
// cloud_config_eligible_auth. Only a read failure is an error; a file Codex
// cannot load is undecidable. Nothing read from the file reaches an error or
// warning.
func readCloudAuth(path string) (cloudAuth, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path constructed from the resolved codex home
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cloudAuth{}, nil
		}
		return cloudAuth{}, fmt.Errorf("read %s: %w", path, err)
	}
	auth, ok := parseAuthDotJSON(data)
	if !ok {
		return undecidableAuth(authCannotLoad), nil
	}
	switch resolvedAuthMode(auth) {
	case authModeAPIKey:
		return materialOrCannotLoad(auth.hasOpenAIAPIKey, cloudAuth{}), nil
	case authModeBedrockAPIKey:
		return materialOrCannotLoad(auth.hasBedrockAPIKey, cloudAuth{}), nil
	case authModeBedrockAccessKeys:
		return materialOrCannotLoad(auth.hasBedrockAccessKeys, cloudAuth{}), nil
	case authModePersonalAccessToken:
		return materialOrCannotLoad(auth.hasPersonalAccessToken, undecidableAuth(
			"auth.json uses personal-access-token auth, whose plan and account Codex resolves over the network",
		)), nil
	case authModeAgentIdentity:
		return agentIdentityCloudAuth(auth.agentIdentity), nil
	case authModeChatGPT, authModeChatGPTAuthTokens:
		return chatgptCloudAuth(auth.tokens), nil
	default: // headers: Codex refuses to load it from auth storage.
		return undecidableAuth(authCannotLoad), nil
	}
}

// materialOrCannotLoad mirrors from_auth_dot_json refusing a mode whose
// credential field is absent (login/src/auth/manager.rs:322-393).
func materialOrCannotLoad(present bool, loaded cloudAuth) cloudAuth {
	if !present {
		return undecidableAuth(authCannotLoad)
	}
	return loaded
}

// resolvedAuthMode mirrors AuthDotJson::resolved_mode
// (login/src/auth/manager.rs:1744-1761).
func resolvedAuthMode(auth *authDotJSON) string {
	switch {
	case auth.authMode != nil:
		return *auth.authMode
	case auth.hasPersonalAccessToken:
		return authModePersonalAccessToken
	case auth.hasBedrockAPIKey:
		return authModeBedrockAPIKey
	case auth.hasBedrockAccessKeys:
		return authModeBedrockAccessKeys
	case auth.hasOpenAIAPIKey:
		return authModeAPIKey
	default:
		return authModeChatGPT
	}
}

// chatgptCloudAuth applies Codex's ChatGPT branch: the plan and user id are
// claims of the id_token JWT and the account id is a plain token field
// (login/src/auth/manager.rs:599-680). Without token data the plan is
// unknown, so cloud config never applies.
func chatgptCloudAuth(tokens *tokenData) cloudAuth {
	if tokens == nil || !idTokenPlanGetsCloudConfig(tokens.claims.plan) {
		return cloudAuth{}
	}
	return cloudAuth{gate: cloudConfigAppliesToAccount, chatgptUserID: tokens.claims.userID, accountID: tokens.accountID}
}

// agentIdentityCloudAuth applies Codex's agent-identity branch
// (login/src/auth/manager.rs:329-362, login/src/auth/agent_identity.rs:133-153).
// A stored record with a registered task loads without the network, and its
// plan and identity are record fields. A JWT, or a record still needing task
// registration, is resolved over the network. Codex also parses the record's
// private key; cc-port does not read key material.
func agentIdentityCloudAuth(storage *agentIdentityStorage) cloudAuth {
	if storage == nil {
		return undecidableAuth(authCannotLoad)
	}
	record := storage.record
	if record == nil || record.taskID == nil || strings.TrimSpace(*record.taskID) == "" {
		return undecidableAuth(
			"auth.json uses agent-identity auth that Codex registers or verifies over the network",
		)
	}
	if !accountPlanGetsCloudConfig(record.planType) {
		return cloudAuth{}
	}
	return cloudAuth{gate: cloudConfigAppliesToAccount, chatgptUserID: &record.chatgptUserID, accountID: &record.accountID}
}

// parseAuthDotJSON checks auth.json against AuthDotJson's declared fields:
// no duplicate keys, a top-level object, and every declared field of the
// type serde requires. ok is false for any file Codex's deserializer
// rejects. Unknown fields are ignored, as serde ignores them.
func parseAuthDotJSON(data []byte) (auth *authDotJSON, ok bool) {
	fields, ok := jsonObject(data, "auth_mode", "OPENAI_API_KEY", "tokens", "last_refresh", "agent_identity",
		"personal_access_token", "bedrock_api_key", "bedrock_access_keys")
	if !ok {
		return nil, false
	}
	auth = &authDotJSON{}
	var present bool
	if auth.authMode, ok = optionalJSONString(fields["auth_mode"]); !ok ||
		(auth.authMode != nil && !authModes[*auth.authMode]) {
		return nil, false
	}
	for _, field := range []struct {
		name    string
		present *bool
	}{
		{"OPENAI_API_KEY", &auth.hasOpenAIAPIKey},
		{"personal_access_token", &auth.hasPersonalAccessToken},
	} {
		value, valid := optionalJSONString(fields[field.name])
		if !valid {
			return nil, false
		}
		*field.present = value != nil
	}
	if auth.hasBedrockAPIKey, ok = optionalJSONStruct(fields["bedrock_api_key"], map[string]jsonKind{
		"api_key": jsonRequiredString, "region": jsonRequiredString,
	}); !ok {
		return nil, false
	}
	if auth.hasBedrockAccessKeys, ok = optionalJSONStruct(fields["bedrock_access_keys"], map[string]jsonKind{
		"access_key_id": jsonRequiredString, "secret_access_key": jsonRequiredString, "session_token": jsonOptionalString,
	}); !ok {
		return nil, false
	}
	lastRefresh, ok := optionalJSONString(fields["last_refresh"])
	if !ok || (lastRefresh != nil && !validRFC3339(*lastRefresh)) {
		return nil, false
	}
	if auth.tokens, present, ok = parseTokenData(fields["tokens"]); !ok {
		return nil, false
	}
	if !present {
		auth.tokens = nil
	}
	if auth.agentIdentity, ok = parseAgentIdentityStorage(fields["agent_identity"]); !ok {
		return nil, false
	}
	return auth, true
}

func parseTokenData(raw json.RawMessage) (tokens *tokenData, present, ok bool) {
	if isJSONNull(raw) {
		return nil, false, true
	}
	fields, ok := jsonStruct(raw, map[string]jsonKind{
		"id_token": jsonRequiredString, "access_token": jsonRequiredString,
		"refresh_token": jsonRequiredString, authFieldAccountID: jsonOptionalString,
	})
	if !ok {
		return nil, false, false
	}
	var idToken string
	if json.Unmarshal(fields["id_token"], &idToken) != nil {
		return nil, false, false
	}
	claims, ok := parseIDTokenClaims(idToken)
	if !ok {
		return nil, false, false
	}
	accountID, _ := optionalJSONString(fields[authFieldAccountID])
	return &tokenData{claims: claims, accountID: accountID}, true, true
}

func parseAgentIdentityStorage(raw json.RawMessage) (*agentIdentityStorage, bool) {
	if isJSONNull(raw) {
		return nil, true
	}
	var jwt string
	if json.Unmarshal(raw, &jwt) == nil {
		return &agentIdentityStorage{}, true
	}
	fields, ok := jsonStruct(raw, map[string]jsonKind{
		"agent_runtime_id": jsonRequiredString, "agent_private_key": jsonRequiredString,
		authFieldAccountID: jsonRequiredString, authFieldChatGPTUserID: jsonRequiredString,
		authFieldEmail: jsonOptionalString, "plan_type": jsonRequiredString,
		"chatgpt_account_is_fedramp": jsonRequiredBool, "task_id": jsonOptionalString,
	})
	if !ok {
		return nil, false
	}
	record := &agentIdentityRecord{}
	for field, target := range map[string]*string{
		authFieldAccountID: &record.accountID, authFieldChatGPTUserID: &record.chatgptUserID, "plan_type": &record.planType,
	} {
		if json.Unmarshal(fields[field], target) != nil {
			return nil, false
		}
	}
	record.taskID, _ = optionalJSONString(fields["task_id"])
	return &agentIdentityStorage{record: record}, true
}

// The id_token claim names IdClaims declares (login/src/token_data.rs:70-79).
const (
	idTokenAuthClaim    = "https://api.openai.com/auth"    //nolint:gosec // G101: a JWT claim name, not a credential
	idTokenProfileClaim = "https://api.openai.com/profile" //nolint:gosec // G101: a JWT claim name, not a credential
)

// idTokenClaims holds the claims the gate compares; the rest are only
// type-checked.
type idTokenClaims struct {
	plan   *string
	userID *string
}

// parseIDTokenClaims decodes the JWT payload the way decode_jwt_payload and
// parse_chatgpt_jwt_claims do (login/src/token_data.rs:70-198): three
// non-empty dot-separated parts, an unpadded base64url payload holding
// IdClaims, whose declared fields must have their types. The signature is
// not verified; Codex does not verify it either. ok is false for a token
// Codex cannot parse.
func parseIDTokenClaims(jwt string) (claims idTokenClaims, ok bool) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return idTokenClaims{}, false
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return idTokenClaims{}, false
	}
	fields, ok := jsonObject(payload, authFieldEmail, idTokenProfileClaim, idTokenAuthClaim)
	if !ok || !validJSONFields(fields, map[string]jsonKind{authFieldEmail: jsonOptionalString}) {
		return idTokenClaims{}, false
	}
	if !isJSONNull(fields[idTokenProfileClaim]) {
		profile, isObject := jsonObject(fields[idTokenProfileClaim], authFieldEmail)
		if !isObject || !validJSONFields(profile, map[string]jsonKind{authFieldEmail: jsonOptionalString}) {
			return idTokenClaims{}, false
		}
	}
	if isJSONNull(fields[idTokenAuthClaim]) {
		return idTokenClaims{}, true
	}
	auth, isObject := jsonStruct(fields[idTokenAuthClaim], map[string]jsonKind{
		"chatgpt_plan_type": jsonOptionalString, authFieldChatGPTUserID: jsonOptionalString,
		"user_id": jsonOptionalString, "chatgpt_account_id": jsonOptionalString,
		"chatgpt_account_is_fedramp": jsonOptionalBool,
	})
	if !isObject {
		return idTokenClaims{}, false
	}
	claims.plan, _ = optionalJSONString(auth["chatgpt_plan_type"])
	claims.userID, _ = optionalJSONString(auth[authFieldChatGPTUserID])
	if claims.userID == nil {
		claims.userID, _ = optionalJSONString(auth["user_id"])
	}
	return claims, true
}

// idTokenPlanGetsCloudConfig mirrors cloud_config_eligible_auth's plan test
// (cloud-config/src/service.rs:50-58) for an id_token plan: business-like,
// education-like, or enterprise, spelled as serde deserializes KnownPlan
// including its aliases (protocol/src/auth.rs:99-126,
// protocol/src/account.rs:67-80).
func idTokenPlanGetsCloudConfig(plan *string) bool {
	if plan == nil {
		return false
	}
	switch *plan {
	case "education", "hc":
		return true
	default:
		return accountPlanGetsCloudConfig(*plan)
	}
}

// accountPlanGetsCloudConfig is the same test for an agent-identity record's
// plan_type, which serde reads as the account PlanType: its names carry no
// aliases, and any other string is Unknown (protocol/src/account.rs:9-44).
func accountPlanGetsCloudConfig(plan string) bool {
	switch plan {
	case "business", "ent26", "enterprise_cbp_automation", "enterprise_cbp_usage_based",
		"edu", "edu_plus", "edu_pro", "enterprise":
		return true
	default:
		return false
	}
}

// jsonKind is the serde type of one declared field.
type jsonKind int

const (
	jsonRequiredString jsonKind = iota
	jsonOptionalString
	jsonRequiredBool
	// jsonOptionalBool is a #[serde(default)] bool: absent is allowed, null
	// is not.
	jsonOptionalBool
)

// validJSONFields reports whether every declared field in fields has its
// kind. An Option<String> may be absent or null. A required field may be
// neither: serde rejects null for String and bool, and encoding/json would
// accept it silently. A #[serde(default)] bool may be absent, not null.
func validJSONFields(fields map[string]json.RawMessage, declared map[string]jsonKind) bool {
	for name, kind := range declared {
		raw := fields[name]
		switch kind {
		case jsonRequiredString, jsonRequiredBool:
			if isJSONNull(raw) {
				return false
			}
		case jsonOptionalString:
			if isJSONNull(raw) {
				continue
			}
		case jsonOptionalBool:
			if raw == nil {
				continue
			}
			if isJSONNull(raw) {
				return false
			}
		}
		var valid bool
		switch kind {
		case jsonRequiredString, jsonOptionalString:
			var value string
			valid = json.Unmarshal(raw, &value) == nil
		case jsonRequiredBool, jsonOptionalBool:
			var value bool
			valid = json.Unmarshal(raw, &value) == nil
		}
		if !valid {
			return false
		}
	}
	return true
}

func optionalJSONString(raw json.RawMessage) (value *string, ok bool) {
	if isJSONNull(raw) {
		return nil, true
	}
	var decoded string
	if json.Unmarshal(raw, &decoded) != nil {
		return nil, false
	}
	return &decoded, true
}

// optionalJSONStruct reports whether an optional struct field is present
// and, when it is, that its declared fields have their kinds.
func optionalJSONStruct(raw json.RawMessage, declared map[string]jsonKind) (present, ok bool) {
	if isJSONNull(raw) {
		return false, true
	}
	_, ok = jsonStruct(raw, declared)
	return true, ok
}

// jsonStruct decodes raw as a serde struct whose fields are all scalars of
// the declared kinds.
func jsonStruct(raw json.RawMessage, declared map[string]jsonKind) (map[string]json.RawMessage, bool) {
	fields, isObject := jsonObject(raw, slices.Collect(maps.Keys(declared))...)
	if !isObject || !validJSONFields(fields, declared) {
		return nil, false
	}
	return fields, true
}

// jsonObject decodes raw as the object form of a serde struct whose fields
// are declared. serde rejects a repeated declared field and ignores repeated
// unknown ones, while encoding/json keeps the last of either, so ok is false
// when a declared key repeats.
func jsonObject(raw json.RawMessage, declared ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	seen := make(map[string]bool, len(declared))
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		name, _ := key.(string)
		if !slices.Contains(declared, name) {
			continue
		}
		if seen[name] {
			return nil, false
		}
		seen[name] = true
	}
	return fields, true
}

func isJSONNull(raw json.RawMessage) bool {
	return raw == nil || bytes.Equal(raw, []byte("null"))
}

// validRFC3339 accepts the RFC 3339 timestamps chrono writes for a
// DateTime<Utc>, with either a T or a space between date and time.
func validRFC3339(value string) bool {
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339Nano, strings.Replace(value, " ", "T", 1))
	return err == nil
}

// jsonShape describes a serde struct: its declared fields, each with the
// shape of its value when that value is itself a struct or an array of
// structs. A nil field shape is a scalar.
type jsonShape struct {
	fields   map[string]*jsonShape
	elements *jsonShape
}

// cloudCacheShape is CloudConfigBundleCacheFile's struct tree
// (cloud-config/src/cache.rs:198-212, config/src/cloud_config_bundle.rs:24-55,80-85,
// config/src/cloud_config_layers.rs:27-31).
var cloudCacheShape = func() *jsonShape {
	fragments := &jsonShape{elements: &jsonShape{fields: map[string]*jsonShape{"id": nil, "name": nil, "contents": nil}}}
	bucket := &jsonShape{fields: map[string]*jsonShape{"enterprise_managed": fragments}}
	bundle := &jsonShape{fields: map[string]*jsonShape{"config_toml": bucket, "requirements_toml": bucket}}
	payload := &jsonShape{fields: map[string]*jsonShape{
		"version": nil, "cached_at": nil, "expires_at": nil, "chatgpt_user_id": nil, "account_id": nil, "bundle": bundle,
	}}
	return &jsonShape{fields: map[string]*jsonShape{"signed_payload": payload, "signature": nil}}
}()

// repeatedDeclaredField returns the byte offset just past the first key that
// repeats a declared field of its struct, or -1 when none does. serde_json
// rejects a repeated declared field and ignores a repeated unknown one,
// while encoding/json keeps the last of either, so a document serde refuses
// would otherwise read as valid here. base is raw's offset in the document.
func repeatedDeclaredField(raw json.RawMessage, shape *jsonShape, base int64) (int64, error) {
	if shape == nil || isJSONNull(raw) {
		return -1, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return -1, jsonReason(err)
	}
	delim, _ := opening.(json.Delim)
	if (shape.elements != nil && delim != '[') || (shape.elements == nil && delim != '{') {
		// A wrong type is a decode error the typed parse reports.
		return -1, nil
	}
	seen := make(map[string]bool)
	for decoder.More() {
		var child *jsonShape
		if shape.elements != nil {
			child = shape.elements
		} else {
			token, err := decoder.Token()
			if err != nil {
				return -1, jsonReason(err)
			}
			key, _ := token.(string)
			declared, isDeclared := shape.fields[key]
			if isDeclared && seen[key] {
				return base + decoder.InputOffset(), nil
			}
			seen[key] = isDeclared
			child = declared
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return -1, jsonReason(err)
		}
		valueStart := base + decoder.InputOffset() - int64(len(value))
		if offset, err := repeatedDeclaredField(value, child, valueStart); err != nil || offset >= 0 {
			return offset, err
		}
	}
	return -1, nil
}

// plistTopLevelString returns the string value of key in a property list
// whose root is a dictionary, in either the XML or the binary format. found is
// false when the dictionary has no such key; a value of any other type is an
// error, matching Codex's "must be a string" refusal
// (config/src/loader/macos.rs, load_managed_preference_with).
func plistTopLevelString(data []byte, key string) (value string, found bool, err error) {
	if bytes.HasPrefix(data, []byte("bplist00")) {
		return binaryPlistTopLevelString(data, key)
	}
	return xmlPlistTopLevelString(data, key)
}

func xmlPlistTopLevelString(data []byte, key string) (value string, found bool, err error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	root, err := nextXMLElement(decoder)
	if err != nil {
		return "", false, err
	}
	if root.Name.Local != "plist" {
		return "", false, errors.New("root element is not <plist>")
	}
	dict, err := nextXMLElement(decoder)
	if err != nil {
		return "", false, err
	}
	if dict.Name.Local != "dict" {
		return "", false, errors.New("root value is not <dict>")
	}
	// Walk the whole document before answering, so a truncated plist or one
	// with trailing content is refused even when the key appears early.
	for {
		keyElement, end, err := nextXMLElementOrEnd(decoder)
		if err != nil {
			return "", false, err
		}
		if end {
			break
		}
		if keyElement.Name.Local != "key" {
			return "", false, errors.New("a dictionary entry does not start with <key>")
		}
		var name string
		if err := decoder.DecodeElement(&name, &keyElement); err != nil {
			return "", false, fmt.Errorf("read dictionary key: %w", xmlReason(err))
		}
		valueElement, err := nextXMLElement(decoder)
		if err != nil {
			return "", false, err
		}
		if name != key {
			if err := decoder.Skip(); err != nil {
				return "", false, fmt.Errorf("skip a dictionary value: %w", xmlReason(err))
			}
			continue
		}
		if valueElement.Name.Local != "string" {
			return "", false, fmt.Errorf("%s must be a string", key)
		}
		if err := decoder.DecodeElement(&value, &valueElement); err != nil {
			return "", false, fmt.Errorf("read %s: %w", key, xmlReason(err))
		}
		found = true
	}
	if err := requireXMLDocumentEnd(decoder); err != nil {
		return "", false, err
	}
	return value, found, nil
}

// requireXMLDocumentEnd consumes the closing </plist> and refuses anything
// but whitespace, comments, and processing instructions after it.
func requireXMLDocumentEnd(decoder *xml.Decoder) error {
	if _, end, err := nextXMLElementOrEnd(decoder); err != nil {
		return err
	} else if !end {
		return errors.New("unexpected element after the root dictionary")
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return xmlReason(err)
		}
		switch typed := token.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(typed)) != 0 {
				return fmt.Errorf("unexpected text after </plist> at byte %d", decoder.InputOffset())
			}
		case xml.Comment, xml.ProcInst:
		default:
			return fmt.Errorf("unexpected content after </plist>: %T", typed)
		}
	}
}

// xmlReason reduces an encoding/xml error to its line number, because its
// message can quote element names or text from a managed-preference payload.
func xmlReason(err error) error {
	var syntaxErr *xml.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("malformed XML on line %d", syntaxErr.Line)
	}
	return errors.New("malformed XML")
}

func nextXMLElement(decoder *xml.Decoder) (xml.StartElement, error) {
	start, end, err := nextXMLElementOrEnd(decoder)
	if err != nil {
		return xml.StartElement{}, err
	}
	if end {
		return xml.StartElement{}, errors.New("unexpected end element")
	}
	return start, nil
}

// nextXMLElementOrEnd skips the prolog, comments, and whitespace between
// elements, and returns the next start element or reports an end element.
func nextXMLElementOrEnd(decoder *xml.Decoder) (xml.StartElement, bool, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return xml.StartElement{}, false, errors.New("unexpected end of document")
			}
			return xml.StartElement{}, false, xmlReason(err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			return typed, false, nil
		case xml.EndElement:
			return xml.StartElement{}, true, nil
		case xml.CharData:
			if len(bytes.TrimSpace(typed)) != 0 {
				return xml.StartElement{}, false, fmt.Errorf("unexpected text at byte %d", decoder.InputOffset())
			}
		}
	}
}

// binaryPlist addresses the objects of a "bplist00" property list: a
// header, the objects, an offset table, and a 32-byte trailer.
type binaryPlist struct {
	data        []byte
	objectsEnd  uint64
	offsetTable uint64
	offsetSize  uint64
	refSize     uint64
	numObjects  uint64
}

const (
	binaryPlistHeaderLength  = 8
	binaryPlistTrailerLength = 32

	binaryPlistTypeInt         = 0x1
	binaryPlistTypeASCIIString = 0x5
	binaryPlistTypeUTF16String = 0x6
	binaryPlistTypeDict        = 0xD
)

func binaryPlistTopLevelString(data []byte, key string) (value string, found bool, err error) {
	if len(data) < binaryPlistHeaderLength+binaryPlistTrailerLength {
		return "", false, errors.New("binary plist is shorter than its header and trailer")
	}
	trailer := data[len(data)-binaryPlistTrailerLength:]
	plist := binaryPlist{
		data:        data,
		objectsEnd:  uint64(len(data) - binaryPlistTrailerLength), //nolint:gosec // G115: len(data) exceeds the trailer length, checked above
		offsetSize:  uint64(trailer[6]),
		refSize:     uint64(trailer[7]),
		numObjects:  binary.BigEndian.Uint64(trailer[8:16]),
		offsetTable: binary.BigEndian.Uint64(trailer[24:32]),
	}
	topObject := binary.BigEndian.Uint64(trailer[16:24])
	if plist.offsetSize < 1 || plist.offsetSize > 8 || plist.refSize < 1 || plist.refSize > 8 {
		return "", false, errors.New("binary plist trailer declares an invalid integer size")
	}
	if plist.numObjects > plist.objectsEnd || topObject >= plist.numObjects ||
		plist.offsetTable > plist.objectsEnd || plist.numObjects*plist.offsetSize > plist.objectsEnd-plist.offsetTable {
		return "", false, errors.New("binary plist trailer points outside the file")
	}

	objectType, count, start, err := plist.object(topObject)
	if err != nil {
		return "", false, err
	}
	if objectType != binaryPlistTypeDict {
		return "", false, fmt.Errorf("root object has type 0x%x, not a dictionary", objectType)
	}
	refs, err := plist.slice(start, count, 2*plist.refSize)
	if err != nil {
		return "", false, err
	}
	for index := range count {
		keyRef := readBigEndian(refs[index*plist.refSize : (index+1)*plist.refSize])
		name, isString, err := plist.stringObject(keyRef)
		if err != nil {
			return "", false, err
		}
		if !isString {
			return "", false, errors.New("dictionary key is not a string")
		}
		if name != key {
			continue
		}
		valueStart := (count + index) * plist.refSize
		valueRef := readBigEndian(refs[valueStart : valueStart+plist.refSize])
		value, isString, err := plist.stringObject(valueRef)
		if err != nil {
			return "", false, err
		}
		if !isString {
			return "", false, fmt.Errorf("%s must be a string", key)
		}
		return value, true, nil
	}
	return "", false, nil
}

// object returns the type nibble, element count, and body offset of object
// ref. A count nibble of 0xF means the count follows as an integer object.
func (plist binaryPlist) object(ref uint64) (objectType byte, count, start uint64, err error) {
	if ref >= plist.numObjects {
		return 0, 0, 0, fmt.Errorf("binary plist object reference %d is out of range", ref)
	}
	entry := plist.offsetTable + ref*plist.offsetSize
	offset := readBigEndian(plist.data[entry : entry+plist.offsetSize])
	if offset < binaryPlistHeaderLength || offset >= plist.objectsEnd {
		return 0, 0, 0, fmt.Errorf("binary plist object %d starts outside the object area", ref)
	}
	marker := plist.data[offset]
	objectType, count, start = marker>>4, uint64(marker&0x0F), offset+1
	if count != 0x0F {
		return objectType, count, start, nil
	}
	if start >= plist.objectsEnd || plist.data[start]>>4 != binaryPlistTypeInt {
		return 0, 0, 0, fmt.Errorf("binary plist object %d has a malformed length", ref)
	}
	width := uint64(1) << (plist.data[start] & 0x0F)
	if width > 8 {
		return 0, 0, 0, fmt.Errorf("binary plist object %d has a malformed length", ref)
	}
	lengthBytes, err := plist.slice(start+1, 1, width)
	if err != nil {
		return 0, 0, 0, err
	}
	return objectType, readBigEndian(lengthBytes), start + 1 + width, nil
}

// stringObject decodes object ref when it is an ASCII or UTF-16 string;
// isString is false for any other type.
func (plist binaryPlist) stringObject(ref uint64) (value string, isString bool, err error) {
	objectType, count, start, err := plist.object(ref)
	if err != nil {
		return "", false, err
	}
	switch objectType {
	case binaryPlistTypeASCIIString:
		raw, err := plist.slice(start, count, 1)
		if err != nil {
			return "", false, err
		}
		return string(raw), true, nil
	case binaryPlistTypeUTF16String:
		raw, err := plist.slice(start, count, 2)
		if err != nil {
			return "", false, err
		}
		units := make([]uint16, count)
		for index := range units {
			units[index] = binary.BigEndian.Uint16(raw[2*index:])
		}
		return string(utf16.Decode(units)), true, nil
	default:
		return "", false, nil
	}
}

// slice returns count elements of width bytes starting at start, refusing any
// range that leaves the object area.
func (plist binaryPlist) slice(start, count, width uint64) ([]byte, error) {
	if start > plist.objectsEnd || count > (plist.objectsEnd-start)/width {
		return nil, errors.New("binary plist object extends past the object area")
	}
	return plist.data[start : start+count*width], nil
}

func readBigEndian(raw []byte) uint64 {
	var value uint64
	for _, b := range raw {
		value = value<<8 | uint64(b)
	}
	return value
}
