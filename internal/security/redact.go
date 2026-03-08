package security

import "strings"

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

// ScrubMap returns a shallow copy of m with sensitive values replaced by
// "**REDACTED**". Nil input returns nil.
func ScrubMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if IsSensitiveKey(k) {
			out[k] = "**REDACTED**"
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
