package aiscan

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
)

func TestNewClientInitializesAllPublicServiceGroups(t *testing.T) {
	client := NewClient(http.DefaultClient, "http://127.0.0.1:8080", connect.WithProtoJSON())
	if client.Chat == nil || client.Sessions == nil || client.Scans == nil {
		t.Fatalf("client groups = %+v", client)
	}
}
