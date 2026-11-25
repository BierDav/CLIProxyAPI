package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/auth/copilot"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	tr "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const copilotFusionTestResponse = `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"gpt-6-luna","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"pong","annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}`

type copilotFusionUpstream struct {
	t          *testing.T
	selected   string
	refuse     map[string]bool // models refused without a Fusion session
	autoCalls  int
	inferences []string // "model session-token"
}

func (u *copilotFusionUpstream) ctx(parent context.Context) context.Context {
	return context.WithValue(parent, "cliproxy.roundtripper", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		reply := func(status int, payload string) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Request: r, Body: io.NopCloser(strings.NewReader(payload))}, nil
		}
		switch r.URL.Path {
		case "/auto":
			u.autoCalls++
			if got := ojson.GetBytesNoCopy(body, "prompt").String(); got != "Reply with exactly: pong" {
				u.t.Errorf("Fusion prompt = %q", got)
			}
			if r.Header.Get("Copilot-Integration-Id") != "copilot-developer-cli" || r.Header.Get("Authorization") != "Bearer test-credential" {
				u.t.Errorf("Fusion request headers = %v", r.Header)
			}
			return reply(http.StatusOK, `{"session_token":"session-1","selected_model":{"id":"`+u.selected+`"}}`)
		case copilot.Responses:
			model, session := ojson.GetBytesNoCopy(body, "model").String(), r.Header.Get("Copilot-Session-Token")
			u.inferences = append(u.inferences, model+" "+session)
			if session == "" && u.refuse[model] {
				return reply(http.StatusBadRequest, `{"error":{"message":"The requested model is not supported.","code":"model_not_supported","type":"invalid_request_error"}}`)
			}
			if session != "" && r.Header.Get("Copilot-Harness-Id") != "copilot-sdk" {
				u.t.Errorf("Fusion inference headers = %v", r.Header)
			}
			return reply(http.StatusOK, copilotFusionTestResponse)
		}
		u.t.Fatalf("unexpected upstream request %s", r.URL)
		return nil, nil
	}))
}

func copilotFusionTestAuth(models ...string) *cliproxyauth.Auth {
	entries := make([]copilot.Model, 0, len(models))
	for _, id := range models {
		entries = append(entries, copilot.Model{ID: id, Endpoints: []string{copilot.Responses}})
	}
	return &cliproxyauth.Auth{ID: "account", Provider: copilot.Provider, Metadata: map[string]any{"access_token": "test-credential", "base_url": "https://api.individual.githubcopilot.com"}, CopilotCatalog: copilot.NewCatalog(entries, time.Now())}
}

func executeCopilotFusionTest(t *testing.T, u *copilotFusionUpstream, auth *cliproxyauth.Auth, model string) (cliproxyexecutor.Response, error) {
	t.Helper()
	req := cliproxyexecutor.Request{Model: copilot.ModelPrefix + model, Payload: []byte(`{"model":"` + model + `","input":[{"role":"system","content":"be brief"},{"role":"user","content":[{"type":"input_text","text":"Reply with exactly: pong"}]}]}`)}
	opts := cliproxyexecutor.Options{SourceFormat: tr.FormatOpenAIResponse, OriginalRequest: req.Payload}
	e := NewCopilotExecutor(nil)
	opts = e.PlanRequestRoute(auth, req, opts)
	return e.Execute(u.ctx(t.Context()), auth, req, opts)
}

func TestCopilotFusionSelectsModel(t *testing.T) {
	u := &copilotFusionUpstream{t: t, selected: "gpt-6-luna"}
	res, err := executeCopilotFusionTest(t, u, copilotFusionTestAuth("gpt-5.4-nano"), copilot.FusionModel)
	if err != nil {
		t.Fatal(err)
	}
	if u.autoCalls != 1 || len(u.inferences) != 1 || u.inferences[0] != "gpt-6-luna session-1" {
		t.Fatalf("auto=%d inferences=%v, want one Fusion session for gpt-6-luna", u.autoCalls, u.inferences)
	}
	if !strings.Contains(string(res.Payload), "pong") {
		t.Fatalf("response = %s", res.Payload)
	}
}

func TestCopilotExplicitModelRetriesThroughFusion(t *testing.T) {
	u := &copilotFusionUpstream{t: t, selected: "gpt-6-luna", refuse: map[string]bool{"gpt-6-luna": true}}
	if _, err := executeCopilotFusionTest(t, u, copilotFusionTestAuth("gpt-6-luna"), "gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	if u.autoCalls != 1 || strings.Join(u.inferences, ",") != "gpt-6-luna ,gpt-6-luna session-1" {
		t.Fatalf("auto=%d inferences=%v, want a direct refusal then a Fusion retry", u.autoCalls, u.inferences)
	}
}

func TestCopilotExplicitModelNeverSubstituted(t *testing.T) {
	u := &copilotFusionUpstream{t: t, selected: "gpt-6-luna", refuse: map[string]bool{"gpt-6.1-sol": true}}
	_, err := executeCopilotFusionTest(t, u, copilotFusionTestAuth("gpt-6.1-sol"), "gpt-6.1-sol")
	status, ok := err.(statusErr)
	if !ok || status.code != http.StatusBadRequest || ojson.GetBytesNoCopy([]byte(status.msg), "error.code").String() != "model_not_available_for_plan" ||
		!strings.Contains(status.msg, "not available for your plan") {
		t.Fatalf("error = %#v, want model_not_available_for_plan", err)
	}
	if len(u.inferences) != 1 {
		t.Fatalf("inferences = %v, want no substituted request", u.inferences)
	}
}

func TestCopilotSupportedModelSkipsFusion(t *testing.T) {
	u := &copilotFusionUpstream{t: t}
	if _, err := executeCopilotFusionTest(t, u, copilotFusionTestAuth("gpt-5.4-nano"), "gpt-5.4-nano"); err != nil {
		t.Fatal(err)
	}
	if u.autoCalls != 0 || strings.Join(u.inferences, ",") != "gpt-5.4-nano " {
		t.Fatalf("auto=%d inferences=%v, want one direct request", u.autoCalls, u.inferences)
	}
}
