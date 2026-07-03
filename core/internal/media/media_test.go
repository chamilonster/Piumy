// SPDX-License-Identifier: AGPL-3.0-only
package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// fakeClient returns canned bytes instead of hitting a real WhatsApp session.
type fakeClient struct {
	data []byte
	err  error
}

func (f fakeClient) Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error) {
	return f.data, f.err
}

func TestDownloadImage(t *testing.T) {
	dir := t.TempDir()
	d := Downloader{Client: fakeClient{data: []byte("fake-jpeg-bytes")}, Dir: dir}

	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}
	res, err := d.Download(context.Background(), "56999999999@s.whatsapp.net", "msg1", msg)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("got nil Result, want a downloaded file")
	}
	if res.Mime != "image/jpeg" || res.Size != int64(len("fake-jpeg-bytes")) {
		t.Errorf("Result = %+v, want mime=image/jpeg size=%d", res, len("fake-jpeg-bytes"))
	}
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("file not written at %s: %v", res.Path, err)
	}
	if string(got) != "fake-jpeg-bytes" {
		t.Errorf("file content = %q, want %q", got, "fake-jpeg-bytes")
	}
	if rel, err := filepath.Rel(dir, res.Path); err != nil || filepath.IsAbs(rel) || rel[:2] == ".." {
		t.Errorf("Path %q is not under Dir %q", res.Path, dir)
	}
}

func TestDownloadAudio(t *testing.T) {
	dir := t.TempDir()
	d := Downloader{Client: fakeClient{data: []byte("fake-opus-bytes")}, Dir: dir}

	// WhatsApp voice notes (PTT) and sent audio both arrive as audioMessage.
	msg := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus")}}
	res, err := d.Download(context.Background(), "56999999999@s.whatsapp.net", "vn1", msg)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("got nil Result, want a downloaded voice note")
	}
	if res.Mime != "audio/ogg; codecs=opus" {
		t.Errorf("Mime = %q, want audio/ogg; codecs=opus", res.Mime)
	}
	if filepath.Ext(res.Path) != ".ogg" {
		t.Errorf("Path ext = %q, want .ogg", filepath.Ext(res.Path))
	}
	if got, _ := os.ReadFile(res.Path); string(got) != "fake-opus-bytes" {
		t.Errorf("file content = %q, want fake-opus-bytes", got)
	}
}

func TestKind(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, "image"},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{}}, "video"},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}}, "sticker"},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}, "audio"},
		{"text", &waE2E.Message{Conversation: proto.String("hi")}, ""},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		if got := Kind(c.msg); got != c.want {
			t.Errorf("Kind(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDownloadNoMedia(t *testing.T) {
	d := Downloader{Client: fakeClient{}, Dir: t.TempDir()}
	res, err := d.Download(context.Background(), "j", "m", &waE2E.Message{Conversation: proto.String("just text")})
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Errorf("got %+v for a text-only message, want nil", res)
	}
}

func TestSanitizeJID(t *testing.T) {
	cases := map[string]string{
		"56999999999@s.whatsapp.net": "56999999999_s.whatsapp.net",
		"12345-67890@g.us":           "12345-67890_g.us",
		"../../etc/passwd":           ".._.._etc_passwd",
	}
	for in, want := range cases {
		if got := sanitizeJID(in); got != want {
			t.Errorf("sanitizeJID(%q) = %q, want %q", in, got, want)
		}
	}
}
