// Package relay is a client for the Cyber Relay data plane (/v1/*): asset
// search, web search and fetch, hash lookup, captcha solving, SMS numbers and
// the proxy pool. Sub-key management (/v1/keys*) is deliberately absent.
//
// The relay generates its reference, docs/openapi.json, from the route table
// that also serves the API. api.gen.go is generated from that file: the types,
// one Client method per operation and the operation table of the relay
// command, so the client covers exactly what the relay documents. The spec is
// not kept in this repository (cyber-relay is private); regenerating fetches
// it into testdata/, which git ignores:
//
//	go generate ./tools/relay
//
// With the spec present, `go test ./tools/relay` fails when api.gen.go does
// not match it. To follow a newer relay, change -ref below and regenerate.
package relay

//go:generate go run ./internal/gen/cmd -fetch -ref 3b87638bf7117bec65e0d672e383effd68f0e81b
