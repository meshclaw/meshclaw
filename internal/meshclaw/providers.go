package meshclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// callOpenAI calls OpenAI API
func callOpenAI(cfg *Config, message string) string {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "ERROR: OPENAI_API_KEY not set"
	}

	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful assistant."
	}

	model := cfg.Model
	if model == "" {
		model = "gpt-4o"
	}

	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": message},
		},
		"max_tokens": 4096,
	}

	// Add tools if configured
	if len(cfg.Tools) > 0 {
		tools := []map[string]interface{}{}
		for _, t := range cfg.Tools {
			if t == "bash" {
				tools = append(tools, map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":        "bash",
						"description": "Run a bash command and get the output",
						"parameters": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"command": map[string]interface{}{
									"type":        "string",
									"description": "The command to run",
								},
							},
							"required": []string{"command"},
						},
					},
				})
			}
		}
		if len(tools) > 0 {
			reqBody["tools"] = tools
		}
	}

	data, _ := json.Marshal(reqBody)

	cmd := exec.Command("curl", "-s",
		"https://api.openai.com/v1/chat/completions",
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+apiKey,
		"-d", string(data))

	output, err := cmd.Output()
	if err != nil {
		return "ERROR: API call failed: " + err.Error()
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(output, &resp); err != nil {
		return "ERROR: Failed to parse response"
	}

	if resp.Error.Message != "" {
		return "ERROR: " + resp.Error.Message
	}

	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content
	}

	return "(no response)"
}

// SendNotification sends a notification
func SendNotification(cfg *Config, message string) error {
	if cfg.Notify == nil {
		return nil
	}

	switch cfg.Notify.Platform {
	case "telegram":
		return sendTelegram(cfg.Notify.Token, cfg.Notify.ChatID, message)
	case "slack":
		return sendSlack(cfg.Notify.WebhookURL, cfg.Notify.Channel, message)
	case "discord":
		return sendDiscord(cfg.Notify.WebhookURL, message)
	case "webhook":
		return sendWebhook(cfg.Notify.WebhookURL, message)
	}
	return nil
}

func sendTelegram(token, chatID, message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	reqBody := map[string]string{
		"chat_id": chatID,
		"text":    message,
	}
	data, _ := json.Marshal(reqBody)

	cmd := exec.Command("curl", "-s", "-X", "POST", url,
		"-H", "Content-Type: application/json",
		"-d", string(data))
	_, err := cmd.Output()
	return err
}

func sendSlack(webhookURL, channel, message string) error {
	reqBody := map[string]string{
		"text": message,
	}
	if channel != "" {
		reqBody["channel"] = channel
	}
	data, _ := json.Marshal(reqBody)

	cmd := exec.Command("curl", "-s", "-X", "POST", webhookURL,
		"-H", "Content-Type: application/json",
		"-d", string(data))
	_, err := cmd.Output()
	return err
}

func sendDiscord(webhookURL, message string) error {
	reqBody := map[string]string{
		"content": message,
	}
	data, _ := json.Marshal(reqBody)

	cmd := exec.Command("curl", "-s", "-X", "POST", webhookURL,
		"-H", "Content-Type: application/json",
		"-d", string(data))
	_, err := cmd.Output()
	return err
}

func sendWebhook(webhookURL, message string) error {
	reqBody := map[string]string{
		"message": message,
	}
	data, _ := json.Marshal(reqBody)

	cmd := exec.Command("curl", "-s", "-X", "POST", webhookURL,
		"-H", "Content-Type: application/json",
		"-d", string(data))
	_, err := cmd.Output()
	return err
}
