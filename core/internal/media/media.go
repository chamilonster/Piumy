// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package media: downloads image/video/sticker attachments to disk (never
// SQLite — text/metadata in the messages table is the thing that's never
// deleted; media files are the GC target). No new dependency: whatsmeow
// already exposes Client.Download() for exactly this.
package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

// client is the subset of *whatsmeow.Client the downloader needs — narrow on
// purpose so tests can fake it without a live WhatsApp session.
type client interface {
	Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error)
}

// Downloader saves image/video/sticker attachments under Dir, one
// subdirectory per chat JID, named by message ID.
type Downloader struct {
	Client client
	Dir    string
}

// Result is what the caller needs to build a store.Media row.
type Result struct {
	Path string
	Mime string
	Size int64
}

// Download saves the image/video/sticker in m to disk, if present. Returns
// (nil, nil) when the message carries none of those three types — most
// messages (text, other media types out of scope) are a no-op.
func (d Downloader) Download(ctx context.Context, chatJID, msgID string, m *waE2E.Message) (*Result, error) {
	dl, mime, ext := pickDownloadable(m)
	if dl == nil {
		return nil, nil
	}

	data, err := d.Client.Download(ctx, dl)
	if err != nil {
		return nil, fmt.Errorf("media: download: %w", err)
	}

	dir := filepath.Join(d.Dir, sanitizeJID(chatJID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("media: mkdir: %w", err)
	}
	path := filepath.Join(dir, sanitizeJID(msgID)+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("media: write: %w", err)
	}

	return &Result{Path: path, Mime: mime, Size: int64(len(data))}, nil
}

// Kind reports which of the three downloadable media types (if any) m
// carries: "image", "video", "sticker", or "" for anything else. Exposed so
// a caller can decide whether to even attempt a download (0753's per-type/
// per-origin media skip settings) without duplicating the same
// GetImageMessage/GetVideoMessage/GetStickerMessage checks pickDownloadable
// already does — same priority order (a message has at most one anyway).
func Kind(m *waE2E.Message) string {
	switch {
	case m == nil:
		return ""
	case m.GetImageMessage() != nil:
		return "image"
	case m.GetVideoMessage() != nil:
		return "video"
	case m.GetStickerMessage() != nil:
		return "sticker"
	default:
		return ""
	}
}

// pickDownloadable returns the message's image/video/sticker payload (in that
// priority — a message has at most one), its mimetype, and a filename
// extension. The caller is expected to have already unwrapped ephemeral/
// view-once containers (gateway.unwrapMessage) before calling Download.
func pickDownloadable(m *waE2E.Message) (whatsmeow.DownloadableMessage, string, string) {
	if m == nil {
		return nil, "", ""
	}
	if img := m.GetImageMessage(); img != nil {
		return img, img.GetMimetype(), ".jpg"
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return vid, vid.GetMimetype(), ".mp4"
	}
	if sticker := m.GetStickerMessage(); sticker != nil {
		return sticker, sticker.GetMimetype(), ".webp"
	}
	return nil, "", ""
}

// sanitizeJID strips characters that don't belong in a path segment (WhatsApp
// JIDs/IDs are alphanumeric plus a few separators, but never trust external
// input verbatim in a filesystem path).
func sanitizeJID(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}
