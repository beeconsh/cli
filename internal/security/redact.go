package security

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafePath validates that requested is contained within root, preventing
// path traversal attacks. Both paths are resolved to absolute before comparison.
func SafePath(root, requested string) error {
	abs, err := filepath.Abs(filepath.Join(root, requested))
	if err != nil {
		return fmt.Errorf("invalid path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("invalid root")
	}
	if !strings.HasPrefix(abs, absRoot+string(filepath.Separator)) && abs != absRoot {
		return fmt.Errorf("path escapes project root")
	}
	return nil
}

// sensitiveKeys is the canonical set of key base-names that must never appear
// in API responses, audit logs, or UI output.
var sensitiveKeys = map[string]bool{
	"password":          true,
	"secret_value":      true,
	"token":             true,
	"admin_password":    true,
	"secret":            true,
	"secret_key":        true,
	"access_key":        true,
	"api_key":           true,
	"private_key":       true,
	"connection_string": true,
	"client_secret":     true,
	"master_password":   true,
	"auth_token":        true,
	"database_url":      true,
	"connection_url":    true,
	"dsn":               true,
	"ssh_key":           true,
	"credentials":       true,
	"refresh_token":     true,
	"passphrase":        true,
	"encryption_key":    true,
	"signing_key":       true,
	"tls_key":           true,
	"certificate":       true,
	"bearer":            true,
}

// IsSensitiveKey returns true when the base portion (after the last ".")
// of key matches a known sensitive name. The comparison is case-insensitive.
func IsSensitiveKey(key string) bool {
	base := key
	if idx := strings.LastIndex(key, "."); idx >= 0 {
		base = key[idx+1:]
	}
	return sensitiveKeys[strings.ToLower(base)]
}

// ScrubMap returns a deep copy of m with sensitive values replaced by
// "**REDACTED**". Recurses into nested maps. Nil input returns nil.
func ScrubMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if IsSensitiveKey(k) {
			out[k] = "**REDACTED**"
		} else if nested, ok := v.(map[string]interface{}); ok {
			out[k] = ScrubMap(nested)
		} else {
			out[k] = v
		}
	}
	return out
}

// ScrubStringMap returns a shallow copy of m with sensitive values replaced.
// Designed for IntentNode.Intent (map[string]string).
func ScrubStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if IsSensitiveKey(k) {
			out[k] = "**REDACTED**"
		} else {
			out[k] = v
		}
	}
	return out
}

// ScrubChanges returns a shallow copy of m with sensitive diff values replaced.
// Designed for PlanAction.Changes (map[string]string containing "old -> new" diffs).
func ScrubChanges(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if IsSensitiveKey(k) {
			out[k] = "**REDACTED** -> **REDACTED**"
		} else {
			out[k] = v
		}
	}
	return out
}
