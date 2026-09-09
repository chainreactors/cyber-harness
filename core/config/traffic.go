package config

import "fmt"

const (
	DefaultBodyMaxBytes       int64 = 8 << 20
	DefaultBodyRetentionBytes int64 = 2 << 30
)

// Normalize validates local storage policy; remote traffic messages cannot
// alter it. None retains metadata and previews, disk also records body chunks.
func (c TrafficOptions) Normalize() (TrafficOptions, error) {
	if c.BodyStorage == "" {
		c.BodyStorage = "none"
	}
	if c.BodyStorage != "none" && c.BodyStorage != "disk" {
		return c, fmt.Errorf("traffic body_storage must be none or disk")
	}
	if c.BodyMaxBytes < 0 || c.BodyRetentionBytes < 0 {
		return c, fmt.Errorf("traffic body limits must not be negative")
	}
	if c.BodyMaxBytes == 0 {
		c.BodyMaxBytes = DefaultBodyMaxBytes
	}
	if c.BodyRetentionBytes == 0 {
		c.BodyRetentionBytes = DefaultBodyRetentionBytes
	}
	if c.BodyMaxBytes > c.BodyRetentionBytes/2 {
		return c, fmt.Errorf("traffic body_retention_bytes must fit two body_max_bytes")
	}
	return c, nil
}
