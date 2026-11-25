package helps

import (
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// CopilotFusionPrompt returns the latest user message text and whether the
// conversation contains an image. Copilot Fusion routes on these, not on
// system instructions or tool output.
func CopilotFusionPrompt(payload []byte, format translator.Format) (string, bool) {
	root := ojson.ParseBytes(payload)
	path, roleKey, partsKey := "messages", "role", "content"
	switch format {
	case translator.FormatOpenAIResponse, translator.FormatCodex, translator.FormatInteractions:
		path = "input"
	case translator.FormatGemini:
		path, partsKey = "contents", "parts"
	}
	items := root.Get(path)
	if items.Type == ojson.String {
		return items.String(), false
	}
	var prompt string
	var hasImage bool
	items.ForEach(func(_, item ojson.Result) bool {
		role := item.Get(roleKey).String()
		if format == translator.FormatGemini && role == "" {
			role = "user"
		}
		content := item.Get(partsKey)
		var texts []string
		if content.Type == ojson.String {
			texts = append(texts, content.String())
		}
		content.ForEach(func(_, part ojson.Result) bool {
			switch part.Get("type").String() {
			case "text", "input_text":
				texts = append(texts, part.Get("text").String())
			case "image", "image_url", "input_image":
				hasImage = true
			case "":
				if text := part.Get("text"); text.Exists() {
					texts = append(texts, text.String())
				}
				if part.Get("inlineData").Exists() || part.Get("inline_data").Exists() || part.Get("fileData").Exists() {
					hasImage = true
				}
			}
			return true
		})
		if role == "user" && len(texts) > 0 {
			prompt = strings.Join(texts, "\n")
		}
		return true
	})
	return prompt, hasImage
}
