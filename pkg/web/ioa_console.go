package web

import (
	"context"
	"net/http"

	"github.com/chainreactors/ioa/protocols"
)

// IOAConsoleReader is the read-only IOA projection exposed to the authenticated
// AIScan web console. Agent registration and message writes still go through
// the native IOA API and its per-node authentication.
type IOAConsoleReader interface {
	ListNodes(context.Context) ([]protocols.Node, error)
	ListSpaces(context.Context) ([]protocols.SpaceInfo, error)
	ListMessages(context.Context, protocols.MessageFilter) ([]protocols.Message, error)
}

type ioaOverviewResponse struct {
	Nodes    []protocols.Node      `json:"nodes"`
	Spaces   []protocols.SpaceInfo `json:"spaces"`
	Messages []protocols.Message   `json:"messages"`
}

func (h *handlerImpl) ioaOverview(w http.ResponseWriter, r *http.Request) {
	if h.ioa == nil {
		writeError(w, http.StatusServiceUnavailable, "IOA console is unavailable")
		return
	}

	nodes, err := h.ioa.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	spaces, err := h.ioa.ListSpaces(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	messages, err := h.ioa.ListMessages(r.Context(), protocols.MessageFilter{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if nodes == nil {
		nodes = []protocols.Node{}
	}
	if spaces == nil {
		spaces = []protocols.SpaceInfo{}
	}
	if messages == nil {
		messages = []protocols.Message{}
	}
	writeJSON(w, http.StatusOK, ioaOverviewResponse{
		Nodes: nodes, Spaces: spaces, Messages: messages,
	})
}
