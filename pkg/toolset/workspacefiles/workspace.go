package workspacefiles

import (
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
)

type WorkspaceSource interface {
	VirtualFileReader
	VirtualGlobber
}

// Tools constructs the host-workspace tool catalog without registration or IO.
// Unlike bounded files.FS tools, these tools retain absolute-path, invocation
// workdir, image and virtual-source behavior. Install using extensions/toolgroup.
func Tools(workDir string, source WorkspaceSource, audit *fileaudit.Audit, includeList bool) ([]tool.Tool, error) {
	if strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("workspace file tools require a working directory")
	}
	var readers []VirtualFileReader
	var globbers []VirtualGlobber
	if source != nil {
		readers = append(readers, source)
		globbers = append(globbers, source)
	}
	tools := []tool.Tool{
		newWorkspaceReadTool(workDir, audit, readers...),
		newWorkspaceWriteTool(workDir, audit),
	}
	if includeList {
		tools = append(tools, newWorkspaceListTool(workDir))
	}
	tools = append(tools, newWorkspaceGlobTool(workDir, globbers...))
	return tools, nil
}
