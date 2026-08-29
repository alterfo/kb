package web

import (
	"encoding/json"
	"net/http"

	"github.com/alterfo/kb/internal/mcp"
)

type mcpInfoData struct {
	Configured  bool
	Endpoint    string
	StdioCmd    string
	StdioConfig string
	HTTPConfig  string
	Tools       []mcp.ToolInfo
}

type mcpClientConfig struct {
	MCPServers map[string]mcpServerConfig `json:"mcpServers"`
}

type mcpServerConfig struct {
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// handleMCPInfo shows the MCP endpoint URL, tool list, and copy-paste
// client config — the dashboard previously had no page pointing at MCP at
// all, even though internal/mcp already registers a full tool set.
func (s *Server) handleMCPInfo(w http.ResponseWriter, r *http.Request) {
	data := mcpInfoData{
		Configured: s.deps.MCP != nil,
		StdioCmd:   "kb mcp",
	}
	if data.Configured {
		data.Endpoint = "http://" + r.Host + "/mcp"
		data.Tools = s.deps.MCP.Tools()
		data.StdioConfig = mustIndentJSON(mcpClientConfig{
			MCPServers: map[string]mcpServerConfig{"kb": {Command: "kb", Args: []string{"mcp"}}},
		})
		data.HTTPConfig = mustIndentJSON(mcpClientConfig{
			MCPServers: map[string]mcpServerConfig{"kb": {URL: data.Endpoint}},
		})
	}
	s.render(w, "page-mcp-info", http.StatusOK, page{Title: "MCP", Data: data})
}

// mustIndentJSON is safe here: v is always one of the two struct literals
// above, which cannot fail to marshal.
func mustIndentJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}
