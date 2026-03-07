package cloud

import (
	"fmt"
	"regexp"
	"strings"
)

var cloudSpecPattern = regexp.MustCompile(`^([a-zA-Z0-9_-]+)\(region:\s*([a-zA-Z0-9-]+)\)$`)

// ParseSpec parses strings like: aws(region: us-east-1).
func ParseSpec(spec string) (provider, region string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", fmt.Errorf("empty cloud spec")
	}
	m := cloudSpecPattern.FindStringSubmatch(spec)
	if len(m) != 3 {
		return "", "", fmt.Errorf("invalid cloud spec %q (expected provider(region: <region>))", spec)
	}
	return strings.ToLower(m[1]), m[2], nil
}
