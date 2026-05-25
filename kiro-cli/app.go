package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type Request struct {
	Kind    string                 `json:"kind"`
	TypeID  string                 `json:"type_id"`
	Config  map[string]interface{} `json:"config"`
	Context Context                `json:"context"`
}

type Context struct {
	WorkflowID string                 `json:"workflow_id"`
	Iteration  int                    `json:"iteration"`
	Variables  map[string]interface{} `json:"variables"`
}

type Response struct {
	Handle   string                 `json:"handle,omitempty"`
	Response interface{}            `json:"response,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Error    string                 `json:"error,omitempty"`
}

func findKiroPath(userPath string) string {
	if filepath.IsAbs(userPath) {
		return userPath
	}

	if path, err := exec.LookPath(userPath); err == nil {
		return path
	}

	var commonPaths []string

	if runtime.GOOS == "windows" {
		exeName := "kiro-cli.exe"
		commonPaths = []string{
			os.ExpandEnv("$USERPROFILE\\AppData\\Local\\Microsoft\\WindowsApps\\" + exeName),
			os.ExpandEnv("$USERPROFILE\\.cargo\\bin\\" + exeName),
			os.ExpandEnv("$USERPROFILE\\go\\bin\\" + exeName),
		}
	} else {
		commonPaths = []string{
			"/opt/homebrew/bin/kiro-cli",
			"/usr/local/bin/kiro-cli",
			os.ExpandEnv("$HOME/.local/bin/kiro-cli"),
			os.ExpandEnv("$HOME/go/bin/kiro-cli"),
			os.ExpandEnv("$HOME/.cargo/bin/kiro-cli"),
		}
	}

	for _, p := range commonPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return userPath
}

func main() {
	var req Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		writeResponse(Response{Error: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	cfg := req.Config

	rawPath := getStr(cfg, "kiro_path", "kiro-cli")
	kiroPath := findKiroPath(rawPath)
	prompt := getStr(cfg, "prompt", "")
	timeoutSeconds := getNum(cfg, "timeout", 60)

	if prompt == "" {
		writeResponse(Response{
			Handle: "error",
			Error:  "prompt is required",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	cmd := exec.CommandContext(ctx, kiroPath, "chat", "--no-interactive", prompt)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	outputStr := ansiRegex.ReplaceAllString(stdout.String(), "")

	outputStr = strings.TrimSpace(outputStr)
	if after, ok := strings.CutPrefix(outputStr, "> "); ok {
		outputStr = after
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			writeResponse(Response{Handle: "error", Response: outputStr, Error: "timeout"})
			return
		}
		if ctx.Err() == context.Canceled {
			writeResponse(Response{Handle: "error", Response: outputStr, Error: "canceled"})
			return
		}

		writeResponse(Response{
			Handle:   "error",
			Response: outputStr,
			Error:    fmt.Sprintf("%v - %s", err.Error(), stderr.String()),
		})
		return
	}

	writeResponse(Response{
		Handle:   "success",
		Response: outputStr,
		Metadata: map[string]interface{}{
			"type": map[string]string{
				"mime": "text/markdown",
			},
		},
	})
}

func getStr(m map[string]interface{}, key string, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return def
}

func getNum(m map[string]interface{}, key string, def float64) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return def
}

func writeResponse(resp Response) {
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "encode response: %v\n", err)
		os.Exit(1)
	}
}
