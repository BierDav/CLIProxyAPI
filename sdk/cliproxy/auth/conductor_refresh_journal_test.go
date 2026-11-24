package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/auth/codex"
)

// journalingStore is a credential store that also provides the refresh journal,
// as PostgresStore does.
type journalingStore struct {
	codex.RefreshJournal
	claims atomic.Int32
}

func (s *journalingStore) List(context.Context) ([]*Auth, error)           { return nil, nil }
func (s *journalingStore) Save(_ context.Context, a *Auth) (string, error) { return a.ID, nil }
func (s *journalingStore) Delete(context.Context, string) error            { return nil }

func (s *journalingStore) RefreshClaim(ctx context.Context, key string) (bool, error) {
	s.claims.Add(1)
	return s.RefreshJournal.RefreshClaim(ctx, key)
}

type unusableRefreshJournal struct{ codex.RefreshJournal }

func (unusableRefreshJournal) RefreshClaim(context.Context, string) (bool, error) {
	return false, errors.New("process-local journal must not be used")
}

// journaledRefreshExecutor refreshes through the journal bound to ctx, the way
// the Claude and Codex OAuth clients do.
type journaledRefreshExecutor struct{ countingRefreshExecutor }

func (e *journaledRefreshExecutor) Refresh(ctx context.Context, auth *Auth) (*Auth, error) {
	e.refreshCalls.Add(1)
	_, err := codex.JournalRefresh(ctx, unusableRefreshJournal{}, "client", "grant", func() (string, error) {
		return "rotated", nil
	}, func(ojson.Result) (string, error) { return "", errors.New("unexpected receipt") })
	if err != nil {
		return nil, err
	}
	auth.Metadata["access_token"] = "refreshed-token"
	return auth, nil
}

func TestRefreshAuthForRequest_BindsStoreJournalForClaude(t *testing.T) {
	ctx := context.Background()
	store := &journalingStore{RefreshJournal: codex.NewMemoryRefreshJournal()}
	manager := NewManager(store, &RoundRobinSelector{}, nil)
	executor := &journaledRefreshExecutor{countingRefreshExecutor{id: "claude"}}
	manager.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "claude-journal",
		Provider: "claude",
		Metadata: map[string]any{"access_token": "old-token", "refresh_token": "grant"},
	}
	if _, errRegister := manager.Register(ctx, auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	if _, errRefresh := manager.refreshAuthForRequest(ctx, auth.ID, "old-token"); errRefresh != nil {
		t.Fatalf("refreshAuthForRequest() error = %v", errRefresh)
	}
	if executor.refreshCalls.Load() != 1 || store.claims.Load() != 1 {
		t.Fatalf("refresh calls = %d, store claims = %d, want 1 and 1", executor.refreshCalls.Load(), store.claims.Load())
	}
}
