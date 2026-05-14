package spray

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	sdkspray "github.com/chainreactors/sdk/spray"
	spraypkg "github.com/chainreactors/spray/pkg"
)

func TestWithDefaultNoBarAppendsFlag(t *testing.T) {
	got := withDefaultNoBar([]string{"-u", "http://127.0.0.1", "--finger"})
	want := []string{"-u", "http://127.0.0.1", "--finger", "--no-bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withDefaultNoBar() = %#v, want %#v", got, want)
	}
}

func TestWithDefaultNoBarKeepsExplicitFlag(t *testing.T) {
	args := []string{"-u", "http://127.0.0.1", "--no-bar=false"}
	got := withDefaultNoBar(args)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("withDefaultNoBar() = %#v, want %#v", got, args)
	}
}

func TestWithDefaultNoStatAppendsFlag(t *testing.T) {
	got := withDefaultNoStat([]string{"-u", "http://127.0.0.1", "--finger"})
	want := []string{"-u", "http://127.0.0.1", "--finger", "--no-stat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withDefaultNoStat() = %#v, want %#v", got, want)
	}
}

func TestWithDefaultNoStatKeepsExplicitFlag(t *testing.T) {
	args := []string{"-u", "http://127.0.0.1", "--no-stat=false"}
	got := withDefaultNoStat(args)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("withDefaultNoStat() = %#v, want %#v", got, args)
	}
}

func TestWithDefaultScannerFlagsAppendsFlags(t *testing.T) {
	got := withDefaultScannerFlags([]string{"-u", "http://127.0.0.1"})
	want := []string{"-u", "http://127.0.0.1", "--no-bar", "--no-stat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withDefaultScannerFlags() = %#v, want %#v", got, want)
	}
}

func TestExecuteInstallsResourceProviderBeforePrint(t *testing.T) {
	defer spraypkg.ResetResourceProvider()

	var calls atomic.Int32
	engine := sdkspray.NewEngine(sdkspray.NewConfig().WithResourceProvider(func(name string) []byte {
		calls.Add(1)
		switch name {
		case "http", "socket":
			return []byte("[]")
		}
		return nil
	}))

	_, err := New(engine).Execute(context.Background(), []string{"--print"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("resource provider was not called during spray print")
	}
}
