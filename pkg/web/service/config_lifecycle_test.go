package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	profile "github.com/chainreactors/cyber/pkg/profile"
)

func TestConfigShutdownRetainsCandidateWhileCommitIsInProgress(t *testing.T) {
	candidate, _, closed := newRecordingProfile(t)
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	store := &transactionalConfigStore{
		cfg: configForModel("old"), commitEntered: entered, releaseCommit: release,
	}
	svc := NewService(ServiceConfig{
		ConfigStore:  store,
		BuildProfile: func(context.Context, *PreparedConfig) (profile.Profile, error) { return candidate, nil },
	})
	done := make(chan error, 1)
	go func() { _, err := svc.SaveConfig(t.Context(), configForModel("new")); done <- err }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := svc.Close(ctx); !errors.Is(err, extension.ErrCloseIncomplete) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Close during commit = %v", err)
	}
	if closed() {
		t.Fatal("candidate was released while the update still owned it")
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if svc.profile != candidate || svc.pending != nil {
		t.Fatal("committed candidate was not published")
	}
	if err := svc.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !closed() {
		t.Fatal("retry did not close the committed profile")
	}
	if _, err := svc.SaveConfig(t.Context(), configForModel("later")); err == nil {
		t.Fatal("closed service accepted another update")
	}
}

func TestConfigBuilderCannotReturnActiveProfileAsCandidate(t *testing.T) {
	for _, buildErr := range []error{nil, errors.New("builder failed")} {
		t.Run("reused candidate", func(t *testing.T) {
			current, _, closed := newRecordingProfile(t)
			store := &transactionalConfigStore{cfg: configForModel("old")}
			svc := NewService(ServiceConfig{
				Profile: current, ConfigStore: store,
				BuildProfile: func(context.Context, *PreparedConfig) (profile.Profile, error) { return current, buildErr },
			})
			defer svc.Close(context.Background())
			if _, err := svc.SaveConfig(t.Context(), configForModel("new")); err == nil {
				t.Fatal("builder reused the active profile")
			} else if buildErr != nil && !errors.Is(err, buildErr) {
				t.Fatalf("build error lost: %v", err)
			}
			if closed() {
				t.Fatal("candidate cleanup closed the active profile")
			}
			if _, err := current.Providers(); err != nil {
				t.Fatalf("active profile was revoked: %v", err)
			}
			if activeModel(store.cfg) != "old" || svc.pending != nil {
				t.Fatal("rejected candidate changed the transaction")
			}
		})
	}
}

func TestSaveConfigSkipsIdenticalContent(t *testing.T) {
	current, _, closed := newRecordingProfile(t)
	store := &transactionalConfigStore{cfg: configForModel("same")}
	svc := NewService(ServiceConfig{Profile: current, ConfigStore: store,
		BuildProfile: func(context.Context, *PreparedConfig) (profile.Profile, error) {
			t.Fatal("identical configuration rebuilt the profile")
			return nil, nil
		},
	})
	defer svc.Close(context.Background())
	if _, err := svc.SaveConfig(t.Context(), configForModel("same")); err != nil {
		t.Fatal(err)
	}
	if closed() {
		t.Fatal("identical configuration closed the profile")
	}
}

func TestSaveConfigCancelsWorkWithoutWaitingOnItsOwnRequest(t *testing.T) {
	current, _, closed := newRecordingProfile(t)
	candidate, _, _ := newRecordingProfile(t)
	svc := NewService(ServiceConfig{Profile: current, ConfigStore: &transactionalConfigStore{cfg: configForModel("old")},
		BuildProfile: func(context.Context, *PreparedConfig) (profile.Profile, error) { return candidate, nil },
	})
	defer svc.Close(context.Background())
	work, admitted := svc.beginWork()
	if !admitted {
		t.Fatal("work rejected")
	}
	go func() { <-work.Done(); svc.work.Done() }()
	done := make(chan error, 1)
	go func() { _, err := svc.SaveConfig(t.Context(), configForModel("new")); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reload waited on its own request")
	}
	if !closed() || svc.profile != candidate {
		t.Fatal("profile switch did not finish")
	}
}
