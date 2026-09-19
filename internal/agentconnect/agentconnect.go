// Package agentconnect writes agent-connect.json — the one file an agent on
// any machine reads to discover how to reach this gateway. Before this, the
// only copy of the keys lived inside the Windows installer's run-piumy.bat
// (installer/windows/piumy.iss) — parsing a shell script is fragile and
// doesn't exist outside Windows.
package agentconnect

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
)

// Info is the connect contract. No omitempty: an unset key must show up as
// "" so the file reflects reality instead of hiding a missing credential.
type Info struct {
	MCPURL  string `json:"mcp_url"`
	RESTURL string `json:"rest_url"`
	MCPKey  string `json:"mcp_key"`
	RESTKey string `json:"rest_key"`
}

// Params bundles what Write needs.
type Params struct {
	DataDir  string // where agent-connect.json (and status.json) live
	MCPAddr  string // net/http.Server.Addr the MCP server binds, e.g. ":8091"
	RESTAddr string
	MCPKey   string
	RESTKey  string
}

// Write derives the local URLs from MCPAddr/RESTAddr and rewrites
// agent-connect.json in DataDir whole — keys can change between reinstalls,
// no stale field should survive a restart. Atomic (tmp + rename), same
// pattern internal/state.Write uses for status.json in the same directory.
func Write(p Params) error {
	info := Info{
		MCPURL:  localURL(p.MCPAddr, "/mcp"),
		RESTURL: localURL(p.RESTAddr, ""),
		MCPKey:  p.MCPKey,
		RESTKey: p.RESTKey,
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if p.DataDir != "" && p.DataDir != "." {
		if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
			return err
		}
	}
	path := filepath.Join(p.DataDir, "agent-connect.json")
	tmp := path + ".tmp"
	// ponytail: 0600 applies as-is on Unix; on Windows the Go runtime only
	// sets the read-only attribute when the owner-write bit is absent, so
	// this still inherits the folder's normal ACL — the same unencrypted
	// exposure store.db already has sitting in this same directory.
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// localURL turns a bind address ("host:port", host empty/0.0.0.0/:: meaning
// "every interface") into a loopback URL an agent on this same machine can
// actually dial — a wildcard bind address isn't itself dialable.
func localURL(addr, path string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "127.0.0.1", addr
	} else if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + port + path
}
