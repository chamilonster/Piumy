package capiconn

import "testing"

// TestParseConnectorString covers the parseo contract (ct-2026-07-18-1638,
// moved here whole from mcpserver/admin_tools_test.go by ct-2026-07-19-1556):
// tolerant of variable whitespace and field order, a base64 pin with +/=
// survives verbatim. S6 (ct-2026-07-30-031048): the IP now survives too —
// discarding it here (before store.isAllowedPrincipalEndpoint ever saw it)
// is what broke the Raspberry Pi case; validating "is this IP allowed" is
// that function's job, not this parser's.
func TestParseConnectorString(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantIP     string
		wantPort   string
		wantChatID string
		wantPin    string
		wantErr    bool
	}{
		{
			name:       "formato estandar",
			raw:        "192.168.1.83:8787 chat_id:57582399-1400-485c-ab6a-22febe672344 pin:3y+X4bmS0Yau91l/6cJAjw==",
			wantIP:     "192.168.1.83",
			wantPort:   "8787",
			wantChatID: "57582399-1400-485c-ab6a-22febe672344",
			wantPin:    "3y+X4bmS0Yau91l/6cJAjw==",
		},
		{
			name:       "espacios variables",
			raw:        "  192.168.1.83:8787    chat_id:abc-123   pin:xyz==  ",
			wantIP:     "192.168.1.83",
			wantPort:   "8787",
			wantChatID: "abc-123",
			wantPin:    "xyz==",
		},
		{
			name:       "IP de LAN sobrevive — S6, ya no se descarta",
			raw:        "10.0.0.5:9999 chat_id:x pin:y",
			wantIP:     "10.0.0.5",
			wantPort:   "9999",
			wantChatID: "x",
			wantPin:    "y",
		},
		{name: "falta chat_id", raw: "127.0.0.1:8787 pin:y", wantErr: true},
		{name: "puerto no numerico", raw: "192.168.1.83:abc chat_id:x pin:y", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip, port, chatID, pin, err := ParseConnectorString(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParseConnectorString(%q) = nil error, want one", c.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseConnectorString(%q) = %v, want no error", c.raw, err)
			}
			if ip != c.wantIP || port != c.wantPort || chatID != c.wantChatID || pin != c.wantPin {
				t.Errorf("ParseConnectorString(%q) = (%q,%q,%q,%q), want (%q,%q,%q,%q)",
					c.raw, ip, port, chatID, pin, c.wantIP, c.wantPort, c.wantChatID, c.wantPin)
			}
		})
	}
}
