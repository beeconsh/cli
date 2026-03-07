package discovery

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverBeacons scans a repo path for .beecon files.
func DiscoverBeacons(root string) ([]string, error) {
	out := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".beecon" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".beecon") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
