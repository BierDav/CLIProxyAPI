package copilot

import (
	"slices"
	"testing"
	"time"

	"github.com/MoeForge/moego/json"
)

func TestResolveAccountEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, entry, want string
	}{
		{"known model account overrides preference", `{"id":"claude-fable-5.1","supported_endpoints":["/responses"]}`, Responses},
		{"local preference allowed", `{"id":"claude-fable-5.1","supported_endpoints":["/responses","/v1/messages"]}`, Messages},
		{"known model field absent", `{"id":"claude-fable-5.1"}`, Messages},
		{"explicit empty", `{"id":"claude-fable-5.1","supported_endpoints":[]}`, ""},
		{"upstream-only model", `{"id":"new","supported_endpoints":["/chat/completions","/v1/messages","/responses"]}`, Responses},
		{"unknown model without endpoints uses Chat", `{"id":"new"}`, Chat},
		{"unsupported protocol", `{"id":"claude-fable-5.1","supported_endpoints":["ws:/responses","/interactions"]}`, ""},
		{"not authorized", `{"id":"claude-fable-5.1","policy":{"state":"disabled"}}`, ""},
		{"served although hidden from the picker", `{"id":"claude-opus-5","model_picker_enabled":false,"policy":{"state":"enabled"}}`, Messages},
		{"automatic selection is Copilot Fusion", `{"id":"auto","supported_endpoints":["/responses"]}`, Responses},
		{"non-chat model", `{"id":"text-embedding-3-small","capabilities":{"type":"embeddings"}}`, ""},
		{"unconfigured", `{"id":"claude-fable-5.1","policy":{"state":"unconfigured"}}`, ""},
		{"MAI Code 2 prefers Responses", `{"id":"mai-code-2","supported_endpoints":["/chat/completions","/responses"],"policy":{"state":"enabled"}}`, Responses},
		{"MAI Code 2 Flash falls back to local profile", `{"id":"mai-code-2-flash","policy":{"state":"enabled"}}`, Responses},
		{"MAI Code 2 Flash obeys advertised Chat endpoint", `{"id":"mai-code-2-flash","supported_endpoints":["/chat/completions"]}`, Chat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var model Model
			if err := json.Unmarshal([]byte(tc.entry), &model); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			catalog := NewCatalog([]Model{model}, now)
			route, ok := catalog.Active(now).Route(model.ID)
			if got := route.Endpoint; got != tc.want || ok != (tc.want != "") {
				t.Fatalf("route = %q, %v, want %q", got, ok, tc.want)
			}
			if _, okFuture := catalog.Active(now.Add(-time.Second)).Route(model.ID); okFuture {
				t.Fatal("future catalog produced a route")
			}
			if _, okExpired := catalog.Active(now.Add(CatalogMaxAge)).Route(model.ID); okExpired {
				t.Fatal("expired catalog produced a route")
			}
		})
	}
}

func TestOfficialCLIEffortFallbacks(t *testing.T) {
	for _, tc := range []struct {
		id, reasoning string
		want          []string
	}{
		{"gemini-4-argon", "", []string{"low", "medium", "high"}},
		{"kimi-k3", "", []string{"low", "high", "max"}},
		{"gpt-5.6-luna", "", []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{"gpt-5.6-sol", "", []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{"mai-code-2-flash", `["medium"]`, []string{"medium"}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			model := Model{ID: tc.id}
			model.Capabilities.Supports.Reasoning = json.RawMessage(tc.reasoning)
			route, ok := NewCatalog([]Model{model}, time.Now()).Route(model.ID)
			if !ok || route.Info.Thinking == nil || !slices.Equal(route.Info.Thinking.Levels, tc.want) {
				t.Fatalf("unexpected route capabilities: %+v", route.Info)
			}
		})
	}
}

func TestCopilotCLIDefaultsRespectAccountCapabilities(t *testing.T) {
	m := Model{ID: "grok-4.7", DefaultEffort: "low"}
	m.Capabilities.Supports.Reasoning = json.RawMessage(`["low","high"]`)
	if r, _ := NewCatalog([]Model{m}, time.Now()).Route(m.ID); r.DefaultEffort != "low" {
		t.Errorf("account default lost: %q", r.DefaultEffort)
	}
	m.DefaultEffort = ""
	m.Capabilities.Supports.Reasoning = json.RawMessage(`["low"]`)
	if r, _ := NewCatalog([]Model{m}, time.Now()).Route(m.ID); r.DefaultEffort != "" {
		t.Errorf("unsupported CLI effort seeded: %q", r.DefaultEffort)
	}
}

func TestTokenTrustedEndpoint(t *testing.T) {
	var token Token
	token.Value = "proxy-ep=proxy.individual.githubcopilot.com"
	if base, err := token.API(); err != nil || base != defaultAPI {
		t.Fatalf("API = %q, %v", base, err)
	}
	token.Endpoints.API = "https://api.business.githubcopilot.com/"
	if base, err := token.API(); err != nil || base != "https://api.business.githubcopilot.com" {
		t.Fatalf("API = %q, %v", base, err)
	}
	for _, bad := range []string{"http://api.githubcopilot.com", "https://api.githubcopilot.com.evil.test", "https://api.githubcopilot.com@evil.test", "https://api.githubcopilot.com:443", "https://api.githubcopilot.com/path"} {
		token.Endpoints.API = bad
		if _, err := token.API(); err == nil {
			t.Fatalf("accepted endpoint %q", bad)
		}
	}
}

func TestAccountCatalogRoutes(t *testing.T) {
	var models []Model
	for _, entry := range []string{
		`{"id":"gpt-5.4-nano","model_picker_enabled":false,"supported_endpoints":["/responses","ws:/responses"],"capabilities":{"type":"chat"}}`,
		`{"id":"gpt-4o-mini","name":"GPT-4o mini","capabilities":{"type":"chat","limits":{"max_context_window_tokens":128000}}}`,
		`{"id":"gpt-6.1-sol","policy":{"state":"disabled"},"supported_endpoints":["/responses"]}`,
		`{"id":"text-embedding-3-small","capabilities":{"type":"embeddings"}}`,
		`{"id":"gpt-6-luna-decisions","capabilities":{"type":"decisions"},"supported_endpoints":["/v1/decisions"]}`,
	} {
		var model Model
		if err := json.Unmarshal([]byte(entry), &model); err != nil {
			t.Fatal(err)
		}
		models = append(models, model)
	}
	routes := NewCatalog(models, time.Now()).Routes()
	got := map[string]string{}
	for _, route := range routes {
		got[route.Info.ID] = route.Endpoint
	}
	want := map[string]string{"gpt-5.4-nano": Responses, "gpt-4o-mini": Chat, FusionModel: Responses}
	if len(got) != len(want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
	for id, endpoint := range want {
		if got[id] != endpoint {
			t.Fatalf("routes = %v, want %v", got, want)
		}
	}
	mini, _ := NewCatalog(models, time.Now()).Route("gpt-4o-mini")
	if mini.Info.DisplayName != "GPT-4o mini" || mini.Info.ContextLength != 128000 || mini.Info.Type != Provider {
		t.Fatalf("account-only route lost directory capabilities: %+v", mini.Info)
	}
	for _, catalog := range []*Catalog{PresetCatalog, UnavailableCatalog, NewCatalog(nil, time.Now())} {
		if _, ok := catalog.Route(FusionModel); ok {
			t.Fatal("Copilot Fusion was published before the account directory was discovered")
		}
	}
}

func TestFusionSelection(t *testing.T) {
	catalog := NewCatalog([]Model{{ID: "mai-code-2-flash", Endpoints: []string{Chat}}}, time.Now())
	if route := catalog.FusionSelection("gpt-6-luna"); route.Endpoint != Responses || route.Info.ID != "gpt-6-luna" {
		t.Fatalf("model outside the directory = %+v, want a Responses route", route)
	}
	if route := catalog.FusionSelection("mai-code-2-flash"); route.Endpoint != Chat {
		t.Fatalf("directory model = %+v, want its own route", route)
	}
}
