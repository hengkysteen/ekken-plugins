package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestMistralPlugin_Execution(t *testing.T) {
	// 1. Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		
		// If stream requested, send SSE stream
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Write([]byte("data: {\"choices\": [{\"delta\": {\"content\": \"Mocked stream\"}}]}\n\ndata: [DONE]\n\n"))
			return
		}

		// Otherwise send full response
		w.Write([]byte(`{
			"choices": [
				{
					"message": {
						"role": "assistant",
						"content": "Mocked full response"
					}
				}
			]
		}`))
	}))
	defer server.Close()

	// Override API endpoint
	apiURL = server.URL

	// 2. Prepare test inputs and redirect stdin/stdout
	cases := []struct {
		name           string
		input          StdinRequest
		expectedOutput string
	}{
		{
			name: "Non-streaming chat",
			input: StdinRequest{
				Kind:       "assistant",
				ProviderID: "mistral-plugin",
				Request: ChatRequest{
					Model:  "mistral-large-latest",
					Stream: false,
					Messages: []MessageContent{
						{Role: "user", Content: "Hello"},
					},
				},
				Config: map[string]string{
					"api_key": "test-api-key",
				},
			},
			expectedOutput: `"content":"Mocked full response"`,
		},
		{
			name: "Streaming chat",
			input: StdinRequest{
				Kind:       "assistant",
				ProviderID: "mistral-plugin",
				Request: ChatRequest{
					Model:  "mistral-large-latest",
					Stream: true,
					Messages: []MessageContent{
						{Role: "user", Content: "Hello"},
					},
				},
				Config: map[string]string{
					"api_key": "test-api-key",
				},
			},
			expectedOutput: `"content":"Mocked stream"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputBytes, _ := json.Marshal(tc.input)

			// Setup pipes to capture stdout
			oldStdout := os.Stdout
			oldStdin := os.Stdin

			rIn, wIn, _ := os.Pipe()
			rOut, wOut, _ := os.Pipe()

			os.Stdin = rIn
			os.Stdout = wOut

			// Write input to stdin and close write end
			wIn.Write(inputBytes)
			wIn.Write([]byte("\n"))
			wIn.Close()

			mainDone := make(chan bool)
			var capturedOutput string

			go func() {
				// Read output
				var buf bytes.Buffer
				io.Copy(&buf, rOut)
				capturedOutput = buf.String()
				mainDone <- true
			}()

			main()

			// Restore stdout/stdin
			wOut.Close()
			os.Stdout = oldStdout
			os.Stdin = oldStdin

			<-mainDone

			if !strings.Contains(capturedOutput, tc.expectedOutput) {
				t.Errorf("expected output to contain %q, but got %q", tc.expectedOutput, capturedOutput)
			}
			if !strings.Contains(capturedOutput, `"type":"done"`) {
				t.Errorf("expected output to contain done chunk, but got %q", capturedOutput)
			}
		})
	}
}
