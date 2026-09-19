package mcpserver

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestExtractTerminalID(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set(TerminalIDHeader, "term-7")
	ctx := ExtractTerminalID(context.Background(), req)
	if got := terminalIDFromContext(ctx); got != "term-7" {
		t.Errorf("terminalIDFromContext = %q, want %q", got, "term-7")
	}
}

func TestExtractTerminalIDMissingHeader(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)
	ctx := ExtractTerminalID(context.Background(), req)
	if got := terminalIDFromContext(ctx); got != "" {
		t.Errorf("terminalIDFromContext with no header = %q, want empty", got)
	}
}
