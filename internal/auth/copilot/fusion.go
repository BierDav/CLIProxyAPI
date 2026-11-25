package copilot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MoeForge/moego/json"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// FusionModel is Copilot Fusion: GitHub selects a model the account may use
// for each request and authorizes it with a per-request session token.
const FusionModel = "auto"

// FusionRoute is the published Fusion route. Requests are resolved to the
// selected model before translation, so it only carries the client contract.
func FusionRoute() Route {
	return Route{
		Endpoint: Responses,
		Format:   translator.FormatOpenAIResponse,
		Info: &registry.ModelInfo{
			ID: FusionModel, Object: "model", Type: Provider, OwnedBy: Provider,
			DisplayName:              "Copilot Fusion",
			Description:              "GitHub selects a model authorized for this account through Copilot Fusion.",
			ContextLength:            128000,
			InputTokenLimit:          96000,
			OutputTokenLimit:         32000,
			MaxCompletionTokens:      32000,
			SupportedInputModalities: []string{"TEXT", "IMAGE"},
			Thinking:                 &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}},
		},
	}
}

// FusionSession authorizes inference on Model when sent as Copilot-Session-Token.
type FusionSession struct {
	Token string
	Model string
}

// StartFusion asks Copilot Fusion to select a model for the latest user prompt.
// Each request gets its own session; tokens are never stored.
func StartFusion(ctx context.Context, client *http.Client, base, token, prompt string, hasImage bool) (FusionSession, error) {
	if !TrustedAPI(base) || token == "" {
		return FusionSession{}, fmt.Errorf("copilot: missing usable credentials")
	}
	body, err := json.Marshal(map[string]any{
		"prompt": prompt, "has_image": hasImage,
		"multi_turn":          map[string]any{"routing_intent": "anchor", "turns_since_anchor": 0},
		"hydra_rl_multi_turn": map[string]any{},
	})
	if err != nil {
		return FusionSession{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/auto", bytes.NewReader(body))
	if err != nil {
		return FusionSession{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", CLIUserAgent+" client/sdk")
	req.Header.Set("Editor-Version", CLIEditor)
	req.Header.Set("Copilot-Integration-Id", "copilot-developer-cli")
	req.Header.Set("Copilot-Harness-Id", "copilot-sdk")
	req.Header.Set("X-GitHub-Api-Version", "2026-08-01")
	req.Header.Set("Openai-Intent", "conversation-agent")
	req.Header.Set("X-Interaction-Type", "conversation-user")
	req.Header.Set("X-Initiator", "user")
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safeClient.Do(req)
	if err != nil {
		return FusionSession{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return FusionSession{}, HTTPError{resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return FusionSession{}, err
	}
	var session struct {
		Token string `json:"session_token"`
		Model struct {
			ID string `json:"id"`
		} `json:"selected_model"`
	}
	if json.Unmarshal(data, &session) != nil || strings.TrimSpace(session.Token) == "" || strings.TrimSpace(session.Model.ID) == "" {
		return FusionSession{}, fmt.Errorf("copilot: Copilot Fusion returned no authorized model or session token")
	}
	return FusionSession{Token: strings.TrimSpace(session.Token), Model: strings.TrimSpace(session.Model.ID)}, nil
}

// SetFusionHeaders authorizes an inference request through a Fusion session.
func SetFusionHeaders(header http.Header, session string) {
	header.Set("Copilot-Session-Token", session)
	header.Set("Copilot-Harness-Id", "copilot-sdk")
}
