package found

import (
	"github.com/chainreactors/neutron/operators"
	"github.com/chainreactors/neutron/protocols"
	"github.com/chainreactors/proton/proton/file"
)

type ruleSpec struct {
	id       string
	name     string
	severity string
	patterns []string
}

var builtinSpecs = []ruleSpec{
	// Cloud provider keys
	{
		id: "aws-access-key", name: "AWS Access Key", severity: "critical",
		patterns: []string{
			`(?:A3T[A-Z0-9]|AKIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASIA)[A-Z0-9]{16}`,
		},
	},
	{
		id: "aws-secret-key", name: "AWS Secret Key", severity: "critical",
		patterns: []string{
			`(?i)(?:aws_secret_access_key|aws_secret|secret_access_key)\s*[:=]\s*['"]?([A-Za-z0-9/+=]{40})['"]?`,
		},
	},
	{
		id: "gcp-service-account", name: "GCP Service Account Key", severity: "critical",
		patterns: []string{
			`"type"\s*:\s*"service_account"`,
		},
	},
	{
		id: "azure-client-secret", name: "Azure Client Secret", severity: "high",
		patterns: []string{
			`(?i)(?:azure|client)[-_]?secret\s*[:=]\s*['"]?([A-Za-z0-9~._-]{34,})['"]?`,
		},
	},

	// API keys & tokens
	{
		id: "github-token", name: "GitHub Token", severity: "critical",
		patterns: []string{
			`(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{36,}`,
			`github_pat_[A-Za-z0-9_]{22,}`,
		},
	},
	{
		id: "gitlab-token", name: "GitLab Token", severity: "critical",
		patterns: []string{
			`glpat-[A-Za-z0-9\-_]{20,}`,
		},
	},
	{
		id: "slack-token", name: "Slack Token", severity: "high",
		patterns: []string{
			`xox[baprs]-[0-9]{10,}-[0-9a-zA-Z]{10,}`,
		},
	},
	{
		id: "slack-webhook", name: "Slack Webhook", severity: "high",
		patterns: []string{
			`https://hooks\.slack\.com/services/T[A-Z0-9]{8,}/B[A-Z0-9]{8,}/[A-Za-z0-9]{20,}`,
		},
	},
	{
		id: "stripe-key", name: "Stripe Key", severity: "critical",
		patterns: []string{
			`(?:sk|pk)_(?:live|test)_[A-Za-z0-9]{20,}`,
		},
	},
	{
		id: "twilio-key", name: "Twilio API Key", severity: "high",
		patterns: []string{
			`SK[0-9a-fA-F]{32}`,
		},
	},
	{
		id: "sendgrid-key", name: "SendGrid API Key", severity: "high",
		patterns: []string{
			`SG\.[A-Za-z0-9_-]{22,}\.[A-Za-z0-9_-]{22,}`,
		},
	},
	{
		id: "mailgun-key", name: "Mailgun API Key", severity: "high",
		patterns: []string{
			`key-[0-9a-zA-Z]{32}`,
		},
	},

	// Private keys & certificates
	{
		id: "private-key", name: "Private Key", severity: "critical",
		patterns: []string{
			`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY(?:\sBLOCK)?-----`,
		},
	},

	// JWT
	{
		id: "jwt-token", name: "JWT Token", severity: "medium",
		patterns: []string{
			`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`,
		},
	},

	// Database & connection strings
	{
		id: "db-connection-string", name: "Database Connection String", severity: "critical",
		patterns: []string{
			`(?i)(?:mysql|postgres|postgresql|mongodb|redis|amqp|mssql)://[^\s'"<>]{10,}`,
			`(?i)jdbc:[a-z]+://[^\s'"<>]{10,}`,
		},
	},

	// Generic secrets
	{
		id: "generic-api-key", name: "Generic API Key", severity: "medium",
		patterns: []string{
			`(?i)(?:api[_-]?key|apikey)\s*[:=]\s*['"]?([A-Za-z0-9_\-]{20,})['"]?`,
		},
	},
	{
		id: "generic-secret", name: "Generic Secret", severity: "medium",
		patterns: []string{
			`(?i)(?:secret|secret[_-]?key|client[_-]?secret)\s*[:=]\s*['"]?([A-Za-z0-9_\-/+=]{16,})['"]?`,
		},
	},
	{
		id: "generic-password", name: "Password Assignment", severity: "medium",
		patterns: []string{
			`(?i)(?:password|passwd|pwd)\s*[:=]\s*['"]([^'"]{8,})['"]`,
		},
	},
	{
		id: "generic-token", name: "Generic Token", severity: "medium",
		patterns: []string{
			`(?i)(?:access[_-]?token|auth[_-]?token|bearer[_-]?token)\s*[:=]\s*['"]?([A-Za-z0-9_\-/.+=]{20,})['"]?`,
		},
	},

	// Credentials in URLs
	{
		id: "url-credentials", name: "URL with Credentials", severity: "high",
		patterns: []string{
			`(?i)https?://[^:@\s]+:[^:@\s]+@[^\s'"<>]+`,
		},
	},

	// Infrastructure
	{
		id: "ip-with-port", name: "Internal IP with Port", severity: "low",
		patterns: []string{
			`(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}):\d{2,5}`,
		},
	},
}

func builtinRules(execOpts *protocols.ExecuterOptions) []file.Rule {
	var rules []file.Rule
	for _, spec := range builtinSpecs {
		req := &file.Request{Extensions: []string{"all"}}
		var extractors []*operators.Extractor
		for _, pattern := range spec.patterns {
			extractors = append(extractors, &operators.Extractor{
				Type:  "regex",
				Regex: []string{pattern},
			})
		}
		req.Extractors = extractors
		if err := req.Compile(execOpts); err != nil {
			continue
		}
		rules = append(rules, file.Rule{
			ID: spec.id, Name: spec.name,
			Severity: spec.severity, Requests: []*file.Request{req},
		})
	}
	return rules
}
