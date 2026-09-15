package host_test

import (
	"context"
	"fmt"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/pkg/host"
	"google.golang.org/protobuf/proto"
)

func ExampleHost_Handle() {
	mux := aop.NewNamespaceMux(context.Background())
	// An application registers its concrete handlers, without an adapter type.
	err := mux.Register("test", &aop.ProtocolMessage{}, func(_ context.Context, request *aop.Envelope, message proto.Message, send aop.SendFunc) error {
		return send(aop.Reply(request.Id, message))
	})
	if err != nil {
		panic(err)
	}
	h := host.New(mux)
	defer h.Close()
	request := aop.MustWrap("example", "", &aop.ProtocolMessage{})
	err = h.Handle(request, func(response *aop.Envelope) error {
		fmt.Println(response.ReplyTo)
		return nil
	})
	if err != nil {
		panic(err)
	}
	// Output: example
}
