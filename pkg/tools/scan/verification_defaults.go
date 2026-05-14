package scan

import "strings"

func (c *Command) applyVerificationDefaults(flags *flags, args []string) {
	if flags == nil {
		return
	}
	if c.verification.Enable && !hasFlag(args, "--verify") {
		minPriority := strings.TrimSpace(c.verification.MinPriority)
		if minPriority == "" {
			minPriority = "high"
		}
		flags.Verify = minPriority
	}
	if !hasFlag(args, "--verify-timeout") && c.verification.Timeout > 0 {
		flags.VerifyTimeout = c.verification.Timeout
	}
}

func verificationEnabled(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	return mode != "" && mode != "off"
}

func hasFlag(args []string, long string) bool {
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}
