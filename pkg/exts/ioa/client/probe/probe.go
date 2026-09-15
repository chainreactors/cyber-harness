package probe

import (
	"context"
	"errors"
	"fmt"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	clientext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	types "github.com/chainreactors/cyber/pkg/types"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	"strings"
	"time"
)

func Check(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	started := time.Now()
	result := &types.ConnectionCheck{Name: "ioa"}
	run := func() (detail string, resultErr error) {
		read := func(config *types.DistributeConfig) Options {
			if config == nil {
				return Options{}
			}
			if config.Ioa != nil {
				return Options{URL: config.Ioa.Url, Token: config.Ioa.Token}
			}
			sections := cfg.NewSections()
			_ = sections.Register("ioa.client", clientext.Section())
			v, err := sections.Decode(clientext.ConfigKey, cfg.ValuesFromProto(config.Extensions)[clientext.ConfigKey])
			if err != nil {
				return Options{}
			}
			value := v.(*clientext.Options)
			return Options{URL: value.URL, Token: value.Token}
		}
		value, current := read(in), read(stored)
		if strings.TrimSpace(value.URL) == "" {
			value.URL = current.URL
		}
		if strings.TrimSpace(value.Token) == "" {
			value.Token = current.Token
		}
		if value.URL == "" {
			return "", fmt.Errorf("ioa url is empty")
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		client, err := clientext.New(ioatools.Config{URL: value.URL, Token: value.Token}, clientext.Services{})
		if err != nil {
			return "", err
		}
		set, err := extension.New(extension.Entry{ID: "ioa-probe", Extension: client})
		if err != nil {
			return "", err
		}
		defer func() { resultErr = errors.Join(resultErr, set.Close(context.Background())) }()
		if err = set.Load(ctx); err != nil {
			return "", err
		}
		spaces, err := client.Runtime().ListSpaces(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("connected · %d space(s)", len(spaces)), nil
	}
	detail, err := run()
	result.LatencyMs = time.Since(started).Milliseconds()
	result.Detail = detail
	result.Ok = err == nil
	if err != nil {
		result.Error = err.Error()
	}
	return []*types.ConnectionCheck{result}
}

type Options struct{ URL, Token string }
