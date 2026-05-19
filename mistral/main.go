package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type StdinRequest struct {
	Kind       string            `json:"kind"`
	ProviderID string            `json:"provider_id"`
	Request    ChatRequest       `json:"request"`
	Config     map[string]string `json:"config"`
}

type ChatRequest struct {
	ConversationID string           `json:"conversation_id"`
	Model          string           `json:"model"`
	Messages       []MessageContent `json:"messages"`
	Agent          string           `json:"agent,omitempty"`
	Stream         bool             `json:"stream"`
	Thinking       string           `json:"thinking"`
}

type MessageContent struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
	State    string `json:"state,omitempty"`
}

type MistralRequest struct {
	Model    string           `json:"model"`
	Messages []MistralMessage `json:"messages"`
	Stream   bool             `json:"stream"`
}

type MistralMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type MistralChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

type MistralResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

var apiURL = "https://api.mistral.ai/v1/chat/completions"

func main() {
	// Read stdin
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		fmt.Fprintln(os.Stderr, "Error: empty stdin")
		os.Exit(1)
	}
	line := scanner.Bytes()

	var stdinReq StdinRequest
	if err := json.Unmarshal(line, &stdinReq); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid stdin: %v\n", err)
		os.Exit(1)
	}

	apiKey := stdinReq.Config["API_KEY"]
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: API_KEY is required in config")
		os.Exit(1)
	}

	// Prepare Mistral request
	mistralReq := MistralRequest{
		Model:  stdinReq.Request.Model,
		Stream: stdinReq.Request.Stream,
	}
	for _, msg := range stdinReq.Request.Messages {
		mistralReq.Messages = append(mistralReq.Messages, MistralMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	reqBody, err := json.Marshal(mistralReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to marshal request: %v\n", err)
		os.Exit(1)
	}

	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create request: %v\n", err)
		os.Exit(1)
	}

	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if stdinReq.Request.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: HTTP request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: API returned status %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	if stdinReq.Request.Stream {
		reader := bufio.NewReader(resp.Body)
		for {
			lineStr, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				fmt.Fprintf(os.Stderr, "Error: failed to read stream: %v\n", err)
				os.Exit(1)
			}

			lineStr = strings.TrimSpace(lineStr)
			if lineStr == "" {
				continue
			}

			if !strings.HasPrefix(lineStr, "data:") {
				continue
			}

			dataStr := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
			if dataStr == "[DONE]" {
				break
			}

			var chunk MistralChunk
			if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
				continue
			}

			if len(chunk.Choices) > 0 {
				content := chunk.Choices[0].Delta.Content
				if content != "" {
					out, _ := json.Marshal(map[string]string{
						"type":    "chunk",
						"content": content,
					})
					fmt.Println(string(out))
				}
			}
		}
	} else {
		var mistralResp MistralResponse
		if err := json.NewDecoder(resp.Body).Decode(&mistralResp); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to decode response: %v\n", err)
			os.Exit(1)
		}

		if len(mistralResp.Choices) > 0 {
			msg := mistralResp.Choices[0].Message
			out, _ := json.Marshal(map[string]any{
				"type": "message",
				"message": map[string]string{
					"role":    msg.Role,
					"content": msg.Content,
				},
			})
			fmt.Println(string(out))
		}
	}

	done, _ := json.Marshal(map[string]string{"type": "done"})
	fmt.Println(string(done))
}
