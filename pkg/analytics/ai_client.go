package analytics

import "strings"

// AIClient identifies a reported MCP host, not the model behind that host.
func AIClient(name, agent string) string {
	for _, reported := range []string{name, agent} {
		lower := strings.ToLower(reported)
		if lower == "jan" || strings.HasPrefix(lower, "jan/") || strings.HasPrefix(lower, "jan ") {
			return "Jan"
		}
		for _, candidate := range []struct{ match, client string }{
			{"chatgpt", "ChatGPT"},
			{"claude", "Claude"}, {"anthropic", "Claude"},
			{"grok", "Grok"}, {"xai", "Grok"}, {"x.ai", "Grok"}, {"gemini", "Gemini"},
			{"copilot", "GitHub Copilot / VS Code"}, {"vscode", "GitHub Copilot / VS Code"}, {"visual studio code", "GitHub Copilot / VS Code"},
			{"cursor", "Cursor"}, {"qwen", "Qwen"}, {"kimi", "Kimi"},
			{"deepseek", "DeepSeek"}, {"open webui", "Open WebUI"}, {"open-webui", "Open WebUI"},
			{"lm studio", "LM Studio"}, {"lmstudio", "LM Studio"},
			{"ollama", "Ollama"},
		} {
			if strings.Contains(lower, candidate.match) {
				return candidate.client
			}
		}
	}
	return "Unknown"
}

func AIClientKnown(client string) bool {
	return client != "" && client != "Unknown" && AIClient(client, "") == client
}
