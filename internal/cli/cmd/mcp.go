package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// mcpBridgeTimeout bounds a single forwarded tool call. Comfortably exceeds the
// gateway's own per-action timeouts (make.run's bounded wait, docker's 15-120s)
// so the bridge never gives up before the dev server replies.
const mcpBridgeTimeout = 180 * time.Second

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the MCP bridge so AI agents can drive `hamr dev`",
	Long: `Starts a Model Context Protocol server over stdio that an agent (Claude
Code, Codex, opencode, …) spawns. It forwards tool calls to the running
hamr dev server over its localhost API, authenticating with the per-run token
in .hamr/dev.json.

The bridge advertises exactly the tools the project's [dev.mcp.access] map
exposes; the dev server enforces the same permissions and its runtime
kill-switch on every call. Run this from inside a hamr project, or point it at
one with --project.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runMCP,
}

func init() {
	mcpCmd.Flags().String("project", "", "project root (default: nearest hamr.toml from the working directory)")
}

func runMCP(cmd *cobra.Command, _ []string) error {
	projectFlag, _ := cmd.Flags().GetString("project")
	root, err := resolveProjectRoot(projectFlag)
	if err != nil {
		return err
	}

	cfg, err := devserver.LoadConfig(filepath.Join(root, "hamr.toml"))
	if err != nil {
		return fmt.Errorf("load hamr.toml: %w", err)
	}
	enabled := cfg.Dev.MCP.EnabledTools()

	bridge := &mcpBridge{projectRoot: root, client: &http.Client{Timeout: mcpBridgeTimeout}}
	server := mcp.NewServer(&mcp.Implementation{Name: "hamr-dev", Version: version}, nil)

	registered := 0
	for _, t := range bridgeTools {
		if !enabled[t.name] {
			continue
		}
		server.AddTool(
			&mcp.Tool{Name: t.name, Description: t.description, InputSchema: json.RawMessage(t.inputSchema)},
			bridge.handler(t.name),
		)
		registered++
	}

	// stdout is the MCP transport — status goes to stderr only.
	fmt.Fprintf(os.Stderr, "hamr mcp: serving %d tool(s) for %s\n", registered, root)
	return server.Run(cmd.Context(), &mcp.StdioTransport{})
}

// mcpBridge forwards MCP tool calls to a project's running dev-server gateway.
type mcpBridge struct {
	projectRoot string
	client      *http.Client
}

func (b *mcpBridge) handler(tool string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return b.call(ctx, tool, req.Params.Arguments)
	}
}

// call resolves the live handshake, POSTs the tool's args to the gateway, and
// returns the response. Operational problems (dev not running, gateway off,
// permission denied) come back as tool errors the agent can read, not Go errors
// (which the SDK would surface as protocol failures).
func (b *mcpBridge) call(ctx context.Context, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
	hs, err := devserver.ReadMCPHandshake(b.projectRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return toolErrorf("hamr dev is not running (or MCP is disabled) for %s — start `hamr dev` and enable MCP", b.projectRoot), nil
		}
		return toolErrorf("read handshake: %v", err), nil
	}

	body := args
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	url := strings.TrimRight(hs.ProxyURL, "/") + "/__hamr/mcp/" + tool
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return toolErrorf("build request: %v", err), nil
	}
	httpReq.Header.Set("Authorization", "Bearer "+hs.Token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return toolErrorf("cannot reach hamr dev at %s: %v", hs.ProxyURL, err), nil
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return toolErrorf("read response: %v", err), nil
	}
	if resp.StatusCode != http.StatusOK {
		return toolErrorf("hamr dev returned %d: %s", resp.StatusCode, gatewayErrorMessage(respBody)), nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(respBody)}}}, nil
}

// toolErrorf builds a CallToolResult flagged as an error with a readable message.
func toolErrorf(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}},
	}
}

// gatewayErrorMessage extracts the gateway's {"error":"..."} message, falling
// back to the raw body.
func gatewayErrorMessage(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(body))
}

// resolveProjectRoot returns the project directory: --project if given (must
// contain hamr.toml), otherwise the nearest ancestor of the working directory
// that contains hamr.toml.
func resolveProjectRoot(flag string) (string, error) {
	if flag != "" {
		abs, err := filepath.Abs(flag)
		if err != nil {
			return "", err
		}
		if !fileExists(filepath.Join(abs, "hamr.toml")) {
			return "", fmt.Errorf("no hamr.toml in --project %s", abs)
		}
		return abs, nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "hamr.toml")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no hamr.toml found from the working directory upward; run inside a hamr project or pass --project")
		}
		dir = parent
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
