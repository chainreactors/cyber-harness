package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
	profile "github.com/chainreactors/aiscan/pkg/profile/aiscan"
	types "github.com/chainreactors/aiscan/pkg/types"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"google.golang.org/protobuf/proto"
)

type ConfigStore interface {
	GetDistributeConfig(context.Context) (string, bool, *types.DistributeConfig, error)
	PrepareDistributeConfig(context.Context, *types.DistributeConfig) (*PreparedConfig, error)
	CommitDistributeConfig(context.Context, *PreparedConfig) error
	DiscardDistributeConfig(*PreparedConfig)
}

type PreparedConfig struct {
	Config      *types.DistributeConfig
	RuntimePath string
	TargetPath  string
}

// GetDistributeConfig exposes stored configuration without transferring ownership.
func (s *Service) GetDistributeConfig(ctx context.Context) (string, bool, *types.DistributeConfig, error) {
	if s.configStore == nil {
		return "", false, nil, managementapi.Errorf(managementapi.CodeFailedPrecondition, "config store is not configured")
	}
	return s.configStore.GetDistributeConfig(ctx)
}

func (s *Service) SaveConfig(ctx context.Context, config *types.DistributeConfig) (*types.ConfigView, error) {
	select {
	case s.configGate <- struct{}{}:
		defer func() { <-s.configGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.saveConfig(ctx, config)
}

// closePending runs under configGate. A failed drain keeps the candidate owned.
func (s *Service) closePending(ctx context.Context) error {
	if s.pending == nil {
		return nil
	}
	err := s.pending.Close(ctx)
	if !errors.Is(err, extension.ErrCloseIncomplete) {
		s.pending = nil
	}
	return err
}

func (s *Service) saveConfig(ctx context.Context, config *types.DistributeConfig) (view *types.ConfigView, resultErr error) {
	if s.configStore == nil {
		return nil, managementapi.Errorf(managementapi.CodeFailedPrecondition, "config store is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closing {
		return nil, managementapi.Errorf(managementapi.CodeFailedPrecondition, "config service is closed")
	}
	if err := s.closePending(ctx); err != nil {
		return nil, fmt.Errorf("close previous config candidate: %w", err)
	}
	if err := managementapi.ValidateLLMConfig(config.GetLlm()); err != nil {
		return nil, managementapi.NewError(managementapi.CodeInvalidArgument, err)
	}
	prepared, err := s.configStore.PrepareDistributeConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			s.configStore.DiscardDistributeConfig(prepared)
		}
	}()
	if prepared == nil || prepared.Config == nil {
		return nil, fmt.Errorf("config store returned no prepared config")
	}
	if err := managementapi.ValidateLLMConfig(prepared.Config.GetLlm()); err != nil {
		return nil, managementapi.NewError(managementapi.CodeInvalidArgument, err)
	}
	// Candidate cleanup has its own budget: the request may already be canceled.
	// An unfinished candidate remains owned here for Close or the next Save.
	defer func() {
		if s.pending != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, s.closePending(cleanupCtx))
		}
	}()
	var next *profile.Profile
	if s.buildProfile != nil {
		next, err = s.buildProfile(ctx, prepared)
		if next != nil {
			s.appMu.Lock()
			_, owned := s.profiles[next]
			s.appMu.Unlock()
			if owned {
				return nil, errors.Join(err, fmt.Errorf("profile builder returned an already owned profile"))
			}
		}
		s.pending = next
		if err != nil {
			return nil, managementapi.NewError(managementapi.CodeFailedPrecondition, fmt.Errorf("reload aiscan runtime: %w", err))
		}
		if next == nil {
			return nil, fmt.Errorf("reload aiscan runtime returned no app")
		}
		if _, err := next.App(); err != nil {
			return nil, fmt.Errorf("config candidate is not ready: %w", err)
		}
	}
	if err := s.configStore.CommitDistributeConfig(ctx, prepared); err != nil {
		return nil, err
	}
	committed = true
	if next != nil {
		if err := s.swapProfile(next); err != nil {
			return nil, fmt.Errorf("config committed but activation failed: %w", err)
		}
		s.pending = nil
	}
	if s.agents != nil {
		s.agents.BroadcastConfigReload(prepared.Config)
	}
	path, loaded, current, err := s.GetDistributeConfig(ctx)
	if err != nil {
		return nil, err
	}
	return managementapi.ConfigView(current, path, loaded), nil
}

func (s *Service) ActivateConfig(ctx context.Context, id string) (*types.ConfigView, error) {
	select {
	case s.configGate <- struct{}{}:
		defer func() { <-s.configGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, managementapi.Errorf(managementapi.CodeInvalidArgument, "LLM profile id is required")
	}
	_, _, stored, err := s.GetDistributeConfig(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, profile := range stored.GetLlm().GetProviders() {
		if profile.GetId() == id {
			found = true
			break
		}
	}
	if !found {
		return nil, managementapi.Errorf(managementapi.CodeNotFound, "LLM profile %q was not found", id)
	}
	next := proto.CloneOf(stored)
	if next.Llm == nil {
		next.Llm = &types.LLMConfig{}
	}
	next.Llm.ActiveProfile = id
	return s.saveConfig(ctx, next)
}
