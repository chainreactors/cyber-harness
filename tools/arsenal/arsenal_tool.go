package arsenal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/crtm/pkg/registry"
	coretool "github.com/chainreactors/cyber/core/tool"
)

// command is a pseudo-command invoked via bash:
//
//	bash(command="arsenal list")
//	bash(command="arsenal install nuclei")
//	bash(command="arsenal search port scanner")
type command struct {
	mgr *crtm.Manager
}

func (c *command) Name() string { return "arsenal" }

func (c *command) Usage() string {
	return `arsenal — security tool package manager

Usage:
  arsenal list                             all tools + install status + version
  arsenal search <query>                   find tools by keyword/tag
  arsenal info <name>                      detail + docs + hint + latest version
  arsenal install <name> [--version VER]   install (idempotent, latest by default)
  arsenal update <name> [--version VER]    re-download latest or pinned version
  arsenal remove <name>                    delete installed binary
  arsenal releases <name>                  check latest release tag
  arsenal add <owner/repo> [--name NAME] [--pattern PAT]  register third-party repo

Installed tools become immediately available via bash.
Bundled tools are restored automatically; install uses the bundled version when available.`
}

func (c *command) Run(ctx context.Context, execution *coretool.Execution) (any, error) {
	args := execution.Args
	if len(args) == 0 {
		_, _ = fmt.Fprint(execution.Stdout, c.Usage()+"\n")
		return nil, nil
	}

	action := strings.ToLower(args[0])
	rest := args[1:]

	var result string
	var err error

	switch action {
	case "list", "ls":
		result = formatEntryList(c.mgr.ListTools(), c.mgr)
	case "search", "find":
		result, err = c.search(rest)
	case "info":
		result, err = c.info(rest)
	case "install", "i":
		result, err = c.install(ctx, rest)
	case "update", "upgrade":
		result, err = c.update(ctx, rest)
	case "remove", "rm", "uninstall":
		result, err = c.remove(rest)
	case "releases", "release":
		result, err = c.releases(rest)
	case "add":
		result, err = c.add(rest)
	default:
		return nil, fmt.Errorf("unknown command %q. Run 'arsenal' for usage", action)
	}

	if err != nil {
		return nil, err
	}
	if result != "" {
		_, _ = fmt.Fprint(execution.Stdout, result+"\n")
	}
	return nil, nil
}

// --- subcommands ---

func (c *command) search(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: arsenal search <query>")
	}
	query := strings.Join(args, " ")
	results := c.mgr.Search(query)
	if len(results) == 0 {
		return fmt.Sprintf("No tools found for %q", query), nil
	}
	return formatEntryList(results, c.mgr), nil
}

func (c *command) info(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: arsenal info <name>")
	}
	info, err := c.mgr.GetToolInfo(args[0])
	if err != nil {
		return "", err
	}
	return formatToolInfo(info), nil
}

func (c *command) install(ctx context.Context, args []string) (string, error) {
	name, version := parseNameVersion(args)
	if name == "" {
		return "", fmt.Errorf("usage: arsenal install <name> [--version VER]")
	}

	if c.mgr.IsInstalled(name) {
		ver := c.mgr.InstalledVersion(name)
		if version == "" || strings.TrimPrefix(version, "v") == strings.TrimPrefix(ver, "v") {
			return fmt.Sprintf("%s already installed (%s). Use 'arsenal update %s' to refresh.", name, displayVer(ver), name), nil
		}
	}

	if err := c.mgr.Install(ctx, name, version, nil); err != nil {
		return "", fmt.Errorf("install %s: %w", name, err)
	}

	return c.formatPostInstall(name, "Installed"), nil
}

func (c *command) update(ctx context.Context, args []string) (string, error) {
	name, version := parseNameVersion(args)
	if name == "" {
		return "", fmt.Errorf("usage: arsenal update <name> [--version VER]")
	}

	if version == "" {
		version = "latest"
	}
	if err := c.mgr.Install(ctx, name, version, nil); err != nil {
		return "", fmt.Errorf("update %s: %w", name, err)
	}

	return c.formatPostInstall(name, "Updated"), nil
}

func (c *command) remove(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: arsenal remove <name>")
	}
	name := args[0]
	if !c.mgr.IsInstalled(name) {
		return fmt.Sprintf("%s is not installed.", name), nil
	}
	if err := c.mgr.RemoveTool(name); err != nil {
		return "", fmt.Errorf("remove %s: %w", name, err)
	}
	return fmt.Sprintf("Removed %s.", name), nil
}

func (c *command) releases(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: arsenal releases <name>")
	}
	releases, err := c.mgr.ListReleases(args[0])
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(releases, "", "  ")
	return string(data), nil
}

func (c *command) add(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: arsenal add <owner/repo> [--name NAME] [--pattern PAT]")
	}

	repo := args[0]
	if !strings.Contains(repo, "/") {
		return "", fmt.Errorf("repo must be owner/repo format (e.g. ffuf/ffuf)")
	}

	name, pattern := "", ""
	for i := 1; i < len(args)-1; i++ {
		switch args[i] {
		case "--name", "-n":
			name = args[i+1]
			i++
		case "--pattern", "-p":
			pattern = args[i+1]
			i++
		}
	}
	if pattern == "" {
		pattern = "{name}_{version}_{os}_{arch}.tar.gz"
	}

	entry := registry.ToolEntry{
		Name:         name,
		Repo:         repo,
		AssetPattern: pattern,
	}
	if entry.Name == "" {
		entry.Name = entry.RepoName()
	}

	added, err := c.mgr.AddCustomTool(entry)
	if err != nil {
		return "", fmt.Errorf("add: %w", err)
	}
	if !added {
		return fmt.Sprintf("%s already registered.", repo), nil
	}
	return fmt.Sprintf("Added %s from %s. Run 'arsenal install %s' to install.", entry.Name, repo, entry.Name), nil
}

// --- helpers ---

func (c *command) formatPostInstall(name, verb string) string {
	ver := c.mgr.InstalledVersion(name)
	result := fmt.Sprintf("%s %s (%s). Available via bash.", verb, name, displayVer(ver))
	if entry, ok := c.mgr.Catalog().Find(name); ok {
		if entry.DocsURL != "" {
			result += "\nDocs: " + entry.DocsURL
		}
		if entry.Hint != "" {
			result += "\nHint: " + entry.Hint
		}
	}
	return result
}

func parseNameVersion(args []string) (name, version string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version", "-V":
			if version != "" || i+1 == len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "-") {
				return "", ""
			}
			version = args[i+1]
			i++
		default:
			if name != "" || strings.HasPrefix(args[i], "-") {
				return "", ""
			}
			name = args[i]
		}
	}
	return
}

func displayVer(v string) string {
	if v == "" || v == "installed" {
		return "installed"
	}
	return "v" + strings.TrimPrefix(v, "v")
}

func formatEntryList(entries []registry.ToolEntry, mgr *crtm.Manager) string {
	if len(entries) == 0 {
		return "No tools found."
	}
	var sb strings.Builder
	var nInstalled int
	for _, e := range entries {
		ver := mgr.InstalledVersion(e.Name)
		var status string
		switch ver {
		case "":
			status = "  "
		case "installed":
			status = "* "
			nInstalled++
		default:
			status = "* v" + ver
			nInstalled++
		}
		desc := e.Description
		if desc == "" && len(e.Tags) > 0 {
			desc = strings.Join(e.Tags, ", ")
		}
		sb.WriteString(fmt.Sprintf("%-10s %-18s [%-18s] %s\n", status, e.Name, e.Org(), desc))
	}
	sb.WriteString(fmt.Sprintf("\n%d/%d installed", nInstalled, len(entries)))
	return sb.String()
}

func formatToolInfo(info crtm.ToolInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Name:      %s\n", info.Name))
	sb.WriteString(fmt.Sprintf("Repo:      %s\n", info.Repo))
	sb.WriteString(fmt.Sprintf("Org:       %s\n", info.Org()))
	if info.Description != "" {
		sb.WriteString(fmt.Sprintf("Desc:      %s\n", info.Description))
	}
	if len(info.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("Tags:      %s\n", strings.Join(info.Tags, ", ")))
	}
	if info.Category != "" {
		sb.WriteString(fmt.Sprintf("Category:  %s\n", info.Category))
	}
	if info.DocsURL != "" {
		sb.WriteString(fmt.Sprintf("Docs:      %s\n", info.DocsURL))
	}
	if info.Hint != "" {
		sb.WriteString(fmt.Sprintf("Hint:      %s\n", info.Hint))
	}
	sb.WriteString(fmt.Sprintf("Installed: %v\n", info.Installed))
	if info.InstalledPath != "" {
		sb.WriteString(fmt.Sprintf("Path:      %s\n", info.InstalledPath))
	}
	if info.LatestVersion != "" {
		sb.WriteString(fmt.Sprintf("Latest:    %s\n", info.LatestVersion))
	}
	if info.LatestVersionErr != "" {
		sb.WriteString(fmt.Sprintf("Version check: %s\n", info.LatestVersionErr))
	}
	return sb.String()
}
