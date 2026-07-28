//go:build ignore

// Command mcp-acceptance drives mem-mcp as a real sequential stdio client.
// It deliberately uses only the Go standard library so the process-level
// acceptance test does not introduce another runtime dependency.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	protocolVersion = "2024-11-05"
	memoryContent   = "MCP reaches the canonical HTTP memory service"
	memoryPath      = "/E2E/MCP"
)

var uuidPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError *bool         `json:"isError"`
}

type memoryState struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	LifecycleStatus string `json:"lifecycle_status"`
	StateVersion    int64  `json:"state_version"`
	UsefulCount     int64  `json:"useful_count"`
}

type rememberResult struct {
	Memory   memoryState `json:"memory"`
	Replayed bool        `json:"replayed"`
}

type mutationResult struct {
	Memory   memoryState `json:"memory"`
	Replayed bool        `json:"replayed"`
}

type forgetResult struct {
	MemoryID     string `json:"memory_id"`
	StateVersion int64  `json:"state_version"`
	Replayed     bool   `json:"replayed"`
}

type memoryListResult struct {
	Memories []memoryState `json:"memories"`
}

type contextEvidence struct {
	MemoryID   string `json:"memory_id"`
	SourceKind string `json:"source_kind"`
	Excerpt    string `json:"excerpt"`
}

type contextResult struct {
	Evidence []contextEvidence `json:"evidence"`
}

type scenarioResult struct {
	MemoryID     string `json:"memory_id"`
	StateVersion int64  `json:"state_version"`
}

type mcpClient struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	decoder *json.Decoder
	encoder *json.Encoder
	logFile *os.File
	nextID  int
}

func main() {
	var (
		mcpBinary = flag.String("mcp-binary", "", "path to the mem-mcp binary")
		logPath   = flag.String("log", "", "path for mem-mcp stderr")
	)
	flag.Parse()

	if err := run(*mcpBinary, *logPath); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-acceptance: %v\n", err)
		os.Exit(1)
	}
}

func run(mcpBinary, logPath string) error {
	if strings.TrimSpace(mcpBinary) == "" {
		return errors.New("--mcp-binary is required")
	}
	if strings.TrimSpace(logPath) == "" {
		return errors.New("--log is required")
	}
	for _, name := range []string{"MEM_SERVER", "MEM_TOKEN", "MEM_WORKSPACE"} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := startMCP(ctx, mcpBinary, logPath)
	if err != nil {
		return err
	}
	result, scenarioErr := runScenario(client)
	closeErr := client.close()
	if scenarioErr != nil {
		if closeErr != nil {
			return fmt.Errorf("%v; close mem-mcp: %w", scenarioErr, closeErr)
		}
		return scenarioErr
	}
	if closeErr != nil {
		return closeErr
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("encode acceptance result: %w", err)
	}
	return nil
}

func startMCP(
	ctx context.Context,
	mcpBinary string,
	logPath string,
) (*mcpClient, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open mem-mcp log: %w", err)
	}

	command := exec.CommandContext(ctx, mcpBinary)
	command.Env = os.Environ()
	stdin, err := command.StdinPipe()
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("open mem-mcp stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		stdin.Close()
		logFile.Close()
		return nil, fmt.Errorf("open mem-mcp stdout: %w", err)
	}
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		stdin.Close()
		logFile.Close()
		return nil, fmt.Errorf("start mem-mcp: %w", err)
	}
	return &mcpClient{
		command: command,
		stdin:   stdin,
		decoder: json.NewDecoder(bufio.NewReader(stdout)),
		encoder: json.NewEncoder(stdin),
		logFile: logFile,
		nextID:  1,
	}, nil
}

func (c *mcpClient) close() error {
	closeErr := c.stdin.Close()
	waitErr := c.command.Wait()
	logErr := c.logFile.Close()
	if closeErr != nil {
		return fmt.Errorf("close mem-mcp stdin: %w", closeErr)
	}
	if waitErr != nil {
		return fmt.Errorf("wait for mem-mcp: %w", waitErr)
	}
	if logErr != nil {
		return fmt.Errorf("close mem-mcp log: %w", logErr)
	}
	return nil
}

func (c *mcpClient) request(method string, params any) (json.RawMessage, error) {
	id := c.nextID
	c.nextID++
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.encoder.Encode(request); err != nil {
		return nil, fmt.Errorf("send %s request: %w", method, err)
	}

	for {
		var response rpcResponse
		if err := c.decoder.Decode(&response); err != nil {
			return nil, fmt.Errorf("read %s response: %w", method, err)
		}
		if len(response.ID) == 0 || bytes.Equal(response.ID, []byte("null")) {
			continue
		}
		var responseID int
		if err := json.Unmarshal(response.ID, &responseID); err != nil {
			return nil, fmt.Errorf("decode %s response id: %w", method, err)
		}
		if responseID != id {
			return nil, fmt.Errorf(
				"%s response id = %d, expected %d",
				method,
				responseID,
				id,
			)
		}
		if response.JSONRPC != "2.0" {
			return nil, fmt.Errorf("%s response omitted jsonrpc=2.0", method)
		}
		if response.Error != nil {
			return nil, fmt.Errorf(
				"%s JSON-RPC error %d: %s",
				method,
				response.Error.Code,
				response.Error.Message,
			)
		}
		if len(response.Result) == 0 {
			return nil, fmt.Errorf("%s response omitted result", method)
		}
		return response.Result, nil
	}
}

func (c *mcpClient) notify(method string, params any) error {
	notification := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if err := c.encoder.Encode(notification); err != nil {
		return fmt.Errorf("send %s notification: %w", method, err)
	}
	return nil
}

func (c *mcpClient) callTool(
	name string,
	arguments map[string]any,
) (json.RawMessage, error) {
	raw, err := c.request("tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	var result toolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode %s tool envelope: %w", name, err)
	}
	if result.IsError == nil {
		return nil, fmt.Errorf("%s tool response omitted isError", name)
	}
	if *result.IsError {
		message := "unknown tool error"
		if len(result.Content) > 0 {
			message = result.Content[0].Text
		}
		return nil, fmt.Errorf("%s tool error: %s", name, message)
	}
	if len(result.Content) != 1 ||
		result.Content[0].Type != "text" ||
		!json.Valid([]byte(result.Content[0].Text)) {
		return nil, fmt.Errorf("%s returned invalid JSON text content", name)
	}
	return json.RawMessage(result.Content[0].Text), nil
}

func runScenario(c *mcpClient) (scenarioResult, error) {
	if err := initialize(c); err != nil {
		return scenarioResult{}, err
	}
	if err := requireTools(c); err != nil {
		return scenarioResult{}, err
	}

	rememberRaw, err := c.callTool("mem_remember", map[string]any{
		"content":         memoryContent,
		"kind":            "decision",
		"path":            memoryPath,
		"idempotency_key": "mcp-e2e-remember-v2",
		"source_type":     "agent",
		"agent_id":        "mcp-e2e",
	})
	if err != nil {
		return scenarioResult{}, err
	}
	var remembered rememberResult
	if err := json.Unmarshal(rememberRaw, &remembered); err != nil {
		return scenarioResult{}, fmt.Errorf("decode mem_remember result: %w", err)
	}
	if remembered.Replayed ||
		!uuidPattern.MatchString(remembered.Memory.ID) ||
		remembered.Memory.Path != memoryPath ||
		remembered.Memory.StateVersion != 1 {
		return scenarioResult{}, fmt.Errorf(
			"unexpected mem_remember result: replayed=%t id=%q path=%q version=%d",
			remembered.Replayed,
			remembered.Memory.ID,
			remembered.Memory.Path,
			remembered.Memory.StateVersion,
		)
	}
	memoryID := remembered.Memory.ID

	allRaw, err := listMemories(c, "all")
	if err != nil {
		return scenarioResult{}, err
	}
	if present, err := listContains(allRaw, memoryID); err != nil {
		return scenarioResult{}, err
	} else if !present {
		return scenarioResult{}, errors.New("remembered memory missing from MCP list")
	}

	feedbackRaw, err := c.callTool("mem_feedback", map[string]any{
		"memory_id":        memoryID,
		"action":           "useful",
		"expected_version": 1,
		"idempotency_key":  "mcp-e2e-feedback-v2",
	})
	if err != nil {
		return scenarioResult{}, err
	}
	if err := requireMutation(
		feedbackRaw,
		memoryID,
		"",
		2,
		1,
		"feedback",
	); err != nil {
		return scenarioResult{}, err
	}

	archiveRaw, err := c.callTool("mem_archive", map[string]any{
		"memory_id":        memoryID,
		"expected_version": 2,
		"idempotency_key":  "mcp-e2e-archive-v2",
	})
	if err != nil {
		return scenarioResult{}, err
	}
	if err := requireMutation(
		archiveRaw,
		memoryID,
		"archived",
		3,
		1,
		"archive",
	); err != nil {
		return scenarioResult{}, err
	}
	if err := requireRecallState(c, memoryID, false, "archived"); err != nil {
		return scenarioResult{}, err
	}

	restoreRaw, err := c.callTool("mem_restore", map[string]any{
		"memory_id":        memoryID,
		"expected_version": 3,
		"idempotency_key":  "mcp-e2e-restore-v2",
	})
	if err != nil {
		return scenarioResult{}, err
	}
	if err := requireMutation(
		restoreRaw,
		memoryID,
		"active",
		4,
		1,
		"restore",
	); err != nil {
		return scenarioResult{}, err
	}
	if err := requireRecallState(c, memoryID, true, "active"); err != nil {
		return scenarioResult{}, err
	}

	forgetRaw, err := c.callTool("mem_forget", map[string]any{
		"memory_id":        memoryID,
		"expected_version": 4,
		"reason":           "user_request",
		"idempotency_key":  "mcp-e2e-forget-v2",
		"confirm":          true,
	})
	if err != nil {
		return scenarioResult{}, err
	}
	var forgotten forgetResult
	if err := json.Unmarshal(forgetRaw, &forgotten); err != nil {
		return scenarioResult{}, fmt.Errorf("decode mem_forget result: %w", err)
	}
	if forgotten.MemoryID != memoryID ||
		forgotten.StateVersion != 5 ||
		forgotten.Replayed {
		return scenarioResult{}, fmt.Errorf(
			"unexpected mem_forget result: id=%q version=%d replayed=%t",
			forgotten.MemoryID,
			forgotten.StateVersion,
			forgotten.Replayed,
		)
	}
	if bytes.Contains(forgetRaw, []byte(memoryContent)) {
		return scenarioResult{}, errors.New("forget response leaked memory content")
	}

	afterForget, err := listMemories(c, "all")
	if err != nil {
		return scenarioResult{}, err
	}
	if present, err := listContains(afterForget, memoryID); err != nil {
		return scenarioResult{}, err
	} else if present {
		return scenarioResult{}, errors.New("forgotten memory remained in MCP list")
	}
	if bytes.Contains(afterForget, []byte(memoryContent)) {
		return scenarioResult{}, errors.New("post-forget list leaked memory content")
	}
	contextRaw, err := recallContext(c)
	if err != nil {
		return scenarioResult{}, err
	}
	if present, err := contextContains(contextRaw, memoryID); err != nil {
		return scenarioResult{}, err
	} else if present {
		return scenarioResult{}, errors.New("forgotten memory remained in MCP recall")
	}
	if leaked, err := contextLeaksContent(contextRaw, memoryContent); err != nil {
		return scenarioResult{}, err
	} else if leaked {
		return scenarioResult{}, errors.New("post-forget recall leaked memory content")
	}

	return scenarioResult{MemoryID: memoryID, StateVersion: 5}, nil
}

func initialize(c *mcpClient) error {
	raw, err := c.request("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "mem-process-acceptance",
			"version": "1",
		},
	})
	if err != nil {
		return err
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}
	if result.ProtocolVersion != protocolVersion ||
		result.Capabilities.Tools.ListChanged {
		return fmt.Errorf(
			"unexpected initialize result: protocol=%q listChanged=%t",
			result.ProtocolVersion,
			result.Capabilities.Tools.ListChanged,
		)
	}
	// The initialized notification is sent only after the initialize response
	// has been read and validated above.
	return c.notify("notifications/initialized", map[string]any{})
}

func requireTools(c *mcpClient) error {
	raw, err := c.request("tools/list", map[string]any{})
	if err != nil {
		return err
	}
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode tools/list result: %w", err)
	}
	actual := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		actual = append(actual, tool.Name)
	}
	sort.Strings(actual)
	for _, expected := range []string{
		"mem_archive",
		"mem_context",
		"mem_feedback",
		"mem_forget",
		"mem_memory_list",
		"mem_remember",
		"mem_restore",
	} {
		index := sort.SearchStrings(actual, expected)
		if index == len(actual) || actual[index] != expected {
			return fmt.Errorf("tools/list omitted %s", expected)
		}
	}
	return nil
}

func requireMutation(
	raw json.RawMessage,
	memoryID string,
	lifecycle string,
	version int64,
	usefulCount int64,
	label string,
) error {
	var result mutationResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode %s result: %w", label, err)
	}
	if result.Replayed ||
		result.Memory.ID != memoryID ||
		result.Memory.StateVersion != version ||
		result.Memory.UsefulCount != usefulCount ||
		(lifecycle != "" && result.Memory.LifecycleStatus != lifecycle) {
		return fmt.Errorf(
			"unexpected %s result: replayed=%t id=%q lifecycle=%q version=%d useful=%d",
			label,
			result.Replayed,
			result.Memory.ID,
			result.Memory.LifecycleStatus,
			result.Memory.StateVersion,
			result.Memory.UsefulCount,
		)
	}
	return nil
}

func requireRecallState(
	c *mcpClient,
	memoryID string,
	expectedActive bool,
	expectedLifecycle string,
) error {
	contextRaw, err := recallContext(c)
	if err != nil {
		return err
	}
	contextPresent, err := contextContains(contextRaw, memoryID)
	if err != nil {
		return err
	}
	if contextPresent != expectedActive {
		return fmt.Errorf(
			"memory recall presence after %s = %t, expected %t",
			expectedLifecycle,
			contextPresent,
			expectedActive,
		)
	}

	activeRaw, err := listMemories(c, "active")
	if err != nil {
		return err
	}
	activePresent, err := listContains(activeRaw, memoryID)
	if err != nil {
		return err
	}
	if activePresent != expectedActive {
		return fmt.Errorf(
			"active list presence after %s = %t, expected %t",
			expectedLifecycle,
			activePresent,
			expectedActive,
		)
	}

	lifecycleRaw, err := listMemories(c, expectedLifecycle)
	if err != nil {
		return err
	}
	lifecyclePresent, err := listContains(lifecycleRaw, memoryID)
	if err != nil {
		return err
	}
	if !lifecyclePresent {
		return fmt.Errorf(
			"%s memory missing from its lifecycle-specific list",
			expectedLifecycle,
		)
	}
	return nil
}

func recallContext(c *mcpClient) (json.RawMessage, error) {
	return c.callTool("mem_context", map[string]any{
		"query":       memoryContent,
		"scope":       "/E2E",
		"source":      "memory",
		"memory_kind": "decision",
		"limit":       10,
		"max_chars":   4096,
	})
}

func listMemories(c *mcpClient, lifecycle string) (json.RawMessage, error) {
	return c.callTool("mem_memory_list", map[string]any{
		"scope":     "/E2E",
		"lifecycle": lifecycle,
		"limit":     100,
	})
}

func listContains(raw json.RawMessage, memoryID string) (bool, error) {
	var result memoryListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return false, fmt.Errorf("decode mem_memory_list result: %w", err)
	}
	for _, memory := range result.Memories {
		if memory.ID == memoryID {
			return true, nil
		}
	}
	return false, nil
}

func contextContains(raw json.RawMessage, memoryID string) (bool, error) {
	var result contextResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return false, fmt.Errorf("decode mem_context result: %w", err)
	}
	for _, evidence := range result.Evidence {
		if evidence.SourceKind == "memory" && evidence.MemoryID == memoryID {
			return true, nil
		}
	}
	return false, nil
}

func contextLeaksContent(
	raw json.RawMessage,
	content string,
) (bool, error) {
	var result contextResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return false, fmt.Errorf("decode mem_context result: %w", err)
	}
	for _, evidence := range result.Evidence {
		if strings.Contains(evidence.Excerpt, content) {
			return true, nil
		}
	}
	return false, nil
}
