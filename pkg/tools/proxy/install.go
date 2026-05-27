//go:build !full

package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/proxyclient"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

// InstallGlobalProxy sets the proxy for all in-process scanner globals.
// Returns a restore function and any error encountered.
func InstallGlobalProxy(proxyURLStr string) (func(), error) {
	if proxyURLStr == "" {
		return func() {}, nil
	}
	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return func() {}, fmt.Errorf("install proxy: parse URL: %w", err)
	}
	dial, err := proxyclient.NewClient(proxyURL)
	if err != nil {
		return func() {}, fmt.Errorf("install proxy: create client: %w", err)
	}

	dialContext := func(ctx context.Context, network, address string) (net.Conn, error) {
		return dial.DialContext(ctx, network, address)
	}

	prevGogoTransport := gogopkg.DefaultTransport.DialContext
	prevGogoProxy := gogopkg.ProxyDialTimeout
	prevNeutronProxy := neutronhttp.DefaultOption.Proxy
	prevNeutronTransport := neutronhttp.DefaultTransport.Proxy
	prevNeutronDial := neutronhttp.DefaultTransport.DialContext
	prevZombieProxy := zombiepkg.ProxyDialTimeout

	gogopkg.DefaultTransport.DialContext = dialContext
	gogopkg.ProxyDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return dialContext(ctx, network, address)
	}

	httpProxy := http.ProxyURL(proxyURL)
	neutronhttp.DefaultOption.Proxy = httpProxy
	neutronhttp.DefaultTransport.Proxy = httpProxy
	neutronhttp.DefaultTransport.DialContext = dialContext

	zombiepkg.ProxyDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return dial.DialContext(ctx, network, address)
	}

	return func() {
		gogopkg.DefaultTransport.DialContext = prevGogoTransport
		gogopkg.ProxyDialTimeout = prevGogoProxy
		neutronhttp.DefaultOption.Proxy = prevNeutronProxy
		neutronhttp.DefaultTransport.Proxy = prevNeutronTransport
		neutronhttp.DefaultTransport.DialContext = prevNeutronDial
		zombiepkg.ProxyDialTimeout = prevZombieProxy
	}, nil
}
