module example.com/aiscan-external-client

go 1.25.7

require (
	connectrpc.com/connect v1.20.0
	github.com/chainreactors/aiscan v0.0.0
)

require (
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251222181119-0a764e51fe1b // indirect
	google.golang.org/grpc v1.78.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// Repository-local validation. An external project should remove this line and
// use a released AIScan version instead.
replace github.com/chainreactors/aiscan => ../..
