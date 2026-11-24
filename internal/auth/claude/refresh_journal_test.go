package claude

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/auth/codex"
)

// journalTestAuth answers the token endpoint with respond and fails the
// optional profile lookup, which a successful refresh tolerates.
func journalTestAuth(calls *int32, respond func(*http.Request) (*http.Response, error)) *ClaudeAuth {
	return &ClaudeAuth{httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != RefreshTokenURL {
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header), Request: req}, nil
		}
		atomic.AddInt32(calls, 1)
		return respond(req)
	})}}
}

func journalTestResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}
}

// restartProcess drops all in-process refresh state, as a pod restart or
// failover does; only the journal bound to ctx survives.
func restartProcess() { resetClaudeRefreshState() }

func TestRefreshJournal_RestartedProcessRecoversRotatedTokens(t *testing.T) {
	restartProcess()
	defer restartProcess()
	ctx := codex.WithRefreshJournal(context.Background(), codex.NewMemoryRefreshJournal())
	var calls int32
	auth := journalTestAuth(&calls, func(req *http.Request) (*http.Response, error) {
		return journalTestResponse(req, http.StatusOK, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`), nil
	})

	first, err := auth.RefreshTokens(ctx, "old-refresh")
	if err != nil || first.RefreshToken != "new-refresh" {
		t.Fatalf("first refresh = %+v, %v", first, err)
	}

	// The rotated tokens were never persisted; the next process still holds
	// the consumed refresh token and must not send it again.
	restartProcess()
	second, err := auth.RefreshTokens(ctx, "old-refresh")
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.AccessToken != "new-access" || second.RefreshToken != "new-refresh" || second.Expire == "" {
		t.Fatalf("second refresh did not return the receipt: %+v", second)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected the consumed grant to be sent once, got %d", got)
	}
}

func TestRefreshJournal_AmbiguousOutcomeIsNeverReplayed(t *testing.T) {
	restartProcess()
	defer restartProcess()
	ctx := codex.WithRefreshJournal(context.Background(), codex.NewMemoryRefreshJournal())
	var calls int32
	auth := journalTestAuth(&calls, func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: &errorReader{err: io.ErrUnexpectedEOF}, Header: make(http.Header), Request: req}, nil
	})

	for attempt := 0; attempt < 2; attempt++ {
		_, err := auth.RefreshTokensWithRetry(ctx, "lost-response-refresh", 3)
		if !codex.IsRefreshOutcomeUnknown(err) {
			t.Fatalf("attempt %d: expected an unknown refresh outcome, got %v", attempt, err)
		}
		restartProcess()
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected one refresh request, got %d", got)
	}
}

func TestRefreshJournal_ServerErrorReleasesGrant(t *testing.T) {
	restartProcess()
	defer restartProcess()
	ctx := codex.WithRefreshJournal(context.Background(), codex.NewMemoryRefreshJournal())
	var calls int32
	auth := journalTestAuth(&calls, func(req *http.Request) (*http.Response, error) {
		if atomic.LoadInt32(&calls) == 1 {
			return journalTestResponse(req, http.StatusServiceUnavailable, `{"error":"overloaded"}`), nil
		}
		return journalTestResponse(req, http.StatusOK, `{"access_token":"after-503-access","refresh_token":"after-503-refresh","expires_in":3600}`), nil
	})

	token, err := auth.RefreshTokensWithRetry(ctx, "busy-refresh", 3)
	if err != nil || token.RefreshToken != "after-503-refresh" {
		t.Fatalf("refresh after a 503 = %+v, %v", token, err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected the 503 to be retried once, got %d requests", got)
	}
}

func TestRefreshJournal_InvalidGrantIsRecordedAndStillReported(t *testing.T) {
	restartProcess()
	defer restartProcess()
	ctx := codex.WithRefreshJournal(context.Background(), codex.NewMemoryRefreshJournal())
	var calls int32
	auth := journalTestAuth(&calls, func(req *http.Request) (*http.Response, error) {
		return journalTestResponse(req, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Refresh token not found or invalid"}`), nil
	})

	for attempt := 0; attempt < 2; attempt++ {
		_, err := auth.RefreshTokens(ctx, "revoked-refresh")
		if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
			t.Fatalf("attempt %d: expected invalid_grant, got %v", attempt, err)
		}
		if codex.IsRefreshPreSendError(err) || codex.IsRefreshOutcomeUnknown(err) {
			t.Fatalf("attempt %d: invalid_grant must stay a definitive rejection, got %v", attempt, err)
		}
		restartProcess()
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected one refresh request, got %d", got)
	}
}

type failingRefreshJournal struct{ codex.RefreshJournal }

func (failingRefreshJournal) RefreshClaim(context.Context, string) (bool, error) {
	return false, errors.New("postgres unavailable")
}

func TestRefreshJournal_UnavailableStoreSendsNothing(t *testing.T) {
	restartProcess()
	defer restartProcess()
	ctx := codex.WithRefreshJournal(context.Background(), failingRefreshJournal{})
	var calls int32
	auth := journalTestAuth(&calls, func(req *http.Request) (*http.Response, error) {
		return journalTestResponse(req, http.StatusOK, `{"access_token":"a","refresh_token":"b","expires_in":3600}`), nil
	})

	if _, err := auth.RefreshTokensWithRetry(ctx, "unclaimed-refresh", 3); err == nil {
		t.Fatal("expected refresh to fail while the journal is unavailable")
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected no refresh request without a durable claim, got %d", got)
	}
}
