package config

import "strings"

// LocalSkillPaths selects path-based skill arguments for the local library.
func LocalSkillPaths(selected []string) []string {
	var paths []string
	for _, value := range selected {
		if strings.ContainsAny(value, `/\`) || strings.HasPrefix(value, ".") {
			paths = append(paths, value)
		}
	}
	return paths
}
