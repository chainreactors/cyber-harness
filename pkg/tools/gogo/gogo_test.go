package gogo

import (
	"context"
	"sync/atomic"
	"testing"

	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	sdkgogo "github.com/chainreactors/sdk/gogo"
)

func TestExecuteInstallsResourceProviderBeforePrepare(t *testing.T) {
	defer gogopkg.ResetResourceProvider()

	var calls atomic.Int32
	engine := sdkgogo.NewEngine(sdkgogo.NewConfig().WithResourceProvider(func(string) []byte {
		calls.Add(1)
		return nil
	}))

	_, err := New(engine).Execute(context.Background(), []string{"-P", "extract"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("resource provider was not called during gogo prepare")
	}
}
