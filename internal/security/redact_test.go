package security

import "testing"

func TestIsSensitiveKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"password", true},
		{"secret_value", true},
		{"admin_password", true},
		{"token", true},
		{"secret", true},
		{"secret_key", true},
		{"access_key", true},
		{"api_key", true},
		{"private_key", true},
		{"connection_string", true},
		{"client_secret", true},
		{"master_password", true},
		// dotted keys — base extraction
		{"intent.password", true},
		{"intent.db.secret_value", true},
		{"deep.nested.token", true},
		// non-sensitive
		{"username", false},
		{"engine", false},
		{"intent.runtime", false},
		{"region", false},
	}
	for _, tc := range cases {
		if got := IsSensitiveKey(tc.key); got != tc.want {
			t.Errorf("IsSensitiveKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestScrubMapNil(t *testing.T) {
	if ScrubMap(nil) != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestScrubMap(t *testing.T) {
	m := map[string]interface{}{
		"intent.password":   "s3cret",
		"intent.username":   "admin",
		"intent.token":      "abc123",
		"intent.api_key":    "key-val",
		"intent.runtime":    "go1.21",
		"deep.private_key":  "-----BEGIN RSA-----",
		"connection_string": "postgres://...",
	}
	out := ScrubMap(m)
	if out["intent.password"] != "**REDACTED**" {
		t.Errorf("password not scrubbed: %v", out["intent.password"])
	}
	if out["intent.username"] != "admin" {
		t.Errorf("username should not be scrubbed: %v", out["intent.username"])
	}
	if out["intent.token"] != "**REDACTED**" {
		t.Errorf("token not scrubbed")
	}
	if out["intent.api_key"] != "**REDACTED**" {
		t.Errorf("api_key not scrubbed")
	}
	if out["intent.runtime"] != "go1.21" {
		t.Errorf("runtime should not be scrubbed")
	}
	if out["deep.private_key"] != "**REDACTED**" {
		t.Errorf("private_key not scrubbed")
	}
	if out["connection_string"] != "**REDACTED**" {
		t.Errorf("connection_string not scrubbed")
	}
}

func TestScrubStringMap(t *testing.T) {
	m := map[string]string{
		"password": "secret",
		"runtime":  "go1.21",
	}
	out := ScrubStringMap(m)
	if out["password"] != "**REDACTED**" {
		t.Errorf("password not scrubbed: %v", out["password"])
	}
	if out["runtime"] != "go1.21" {
		t.Errorf("runtime should not be scrubbed")
	}
}

func TestScrubStringMapNil(t *testing.T) {
	if ScrubStringMap(nil) != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestScrubChanges(t *testing.T) {
	m := map[string]string{
		"intent.password": "old123 -> new456",
		"intent.engine":   "postgres -> mysql",
	}
	out := ScrubChanges(m)
	if out["intent.password"] != "**REDACTED** -> **REDACTED**" {
		t.Errorf("password changes not scrubbed: %v", out["intent.password"])
	}
	if out["intent.engine"] != "postgres -> mysql" {
		t.Errorf("engine changes should not be scrubbed")
	}
}

func TestScrubChangesNil(t *testing.T) {
	if ScrubChanges(nil) != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestSafePath(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name      string
		requested string
		wantErr   bool
	}{
		{"valid relative", "infra.beecon", false},
		{"valid subdir", "envs/prod.beecon", false},
		{"traversal attack", "../../etc/passwd", true},
		{"dot-dot in middle", "envs/../../etc/passwd", true},
		{"root itself", ".", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := SafePath(root, tc.requested)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %q, got nil", tc.requested)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tc.requested, err)
			}
		})
	}
}
