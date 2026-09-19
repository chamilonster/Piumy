// Package capiconn parses the cAPI connector line the boss pastes verbatim
// from capi_credentials — shared by the set_capi_connector MCP tool
// (mcpserver) and the dashboard's line-paste endpoint (restapi), so the
// parsing logic exists exactly once.
package capiconn

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseConnectorString parses "<ip:puerto> chat_id:<uuid> pin:<base64>".
// Tolerant to variable whitespace (strings.Fields collapses runs) and field
// order. Returns the ip verbatim (S6, ct-2026-07-30-031048): forcing
// 127.0.0.1 here — before isAllowedPrincipalEndpoint ever saw the real
// value — is what broke the Raspberry Pi case (gateway on the Pi, agent on
// another machine of the same LAN): a real LAN IP was discarded before the
// ONE place that decides "is this endpoint allowed" (store.go's
// isAllowedPrincipalEndpoint) ever got a say. Callers must pass ip straight
// through to that validation, never re-decide the question here.
func ParseConnectorString(raw string) (ip, port, chatID, pin string, err error) {
	for _, field := range strings.Fields(raw) {
		switch {
		case strings.HasPrefix(field, "chat_id:"):
			chatID = strings.TrimPrefix(field, "chat_id:")
		case strings.HasPrefix(field, "pin:"):
			pin = strings.TrimPrefix(field, "pin:")
		case port == "" && strings.Contains(field, ":"):
			if host, p, ok := strings.Cut(field, ":"); ok {
				ip, port = host, p
			}
		}
	}
	if ip == "" || port == "" || chatID == "" || pin == "" {
		return "", "", "", "", fmt.Errorf("connector string inválido — esperado '<ip:puerto> chat_id:<uuid> pin:<base64>', recibido %q", raw)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", "", "", "", fmt.Errorf("puerto inválido en %q: %w", raw, err)
	}
	return ip, port, chatID, pin, nil
}
