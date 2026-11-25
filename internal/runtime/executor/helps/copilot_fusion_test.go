package helps

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestCopilotFusionPrompt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  translator.Format
		payload string
		prompt  string
		image   bool
	}{
		{"responses string", translator.FormatOpenAIResponse, `{"input":"hi"}`, "hi", false},
		{"responses latest user", translator.FormatOpenAIResponse, `{"input":[{"role":"developer","content":"rules"},{"role":"user","content":[{"type":"input_text","text":"first"}]},{"type":"function_call_output","output":"x"},{"role":"user","content":[{"type":"input_text","text":"second"},{"type":"input_image","image_url":"data:"}]}]}`, "second", true},
		{"chat", translator.FormatOpenAI, `{"messages":[{"role":"system","content":"rules"},{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:"}}]},{"role":"tool","content":"result"}]}`, "look", true},
		{"claude", translator.FormatClaude, `{"system":"rules","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]}`, "hello", false},
		{"gemini", translator.FormatGemini, `{"contents":[{"role":"user","parts":[{"text":"gem"},{"inlineData":{"mimeType":"image/png","data":"x"}}]}]}`, "gem", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt, image := CopilotFusionPrompt([]byte(tc.payload), tc.format)
			if prompt != tc.prompt || image != tc.image {
				t.Fatalf("prompt=%q image=%v, want %q %v", prompt, image, tc.prompt, tc.image)
			}
		})
	}
}
