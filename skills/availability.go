//go:build !full

package skills

func skillAvailable(name string) bool {
	switch name {
	case "katana", "passive":
		return false
	default:
		return true
	}
}
