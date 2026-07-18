package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/chainreactors/libcstx/go"
)

func (h *handlerImpl) importSCONodes(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	if err := r.ParseMultipartForm(50 << 20); err != nil { //nolint:gosec // bounded by MaxBytesReader above
		writeError(w, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}

	artifact := strings.TrimSpace(r.FormValue("artifact"))
	if artifact == "" {
		writeError(w, http.StatusBadRequest, "artifact is required")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required: "+err.Error())
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 50<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read file: "+err.Error())
		return
	}

	nodes, err := cstx.Parse(artifact, data)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "transform failed: "+err.Error())
		return
	}

	rawNodes := make([]json.RawMessage, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		id := n.CstxID()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if raw, err := json.Marshal(n); err == nil {
			rawNodes = append(rawNodes, raw)
		}
	}

	scanID := r.FormValue("scan_id")
	if scanID == "" {
		scanID = "import"
	}

	if err := h.service.store.UpsertSCONodes(r.Context(), scanID, rawNodes); err != nil {
		writeError(w, http.StatusInternalServerError, "store: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"nodes":      len(rawNodes),
		"artifact":   artifact,
		"duplicates": len(nodes) - len(rawNodes),
	})
}

func (h *handlerImpl) listSupportedArtifacts(w http.ResponseWriter, _ *http.Request) {
	arts := cstx.SupportedArtifacts()
	if arts == nil {
		arts = []string{}
	}
	writeJSON(w, http.StatusOK, arts)
}
