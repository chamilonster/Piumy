package whatsmeow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

	wmeow "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"

	"piumy-gateway/internal/store"
)

// downloadableRef is whatsmeow.DownloadableMessage plus GetFileLength —
// every concrete media sub-message (Image/Video/Audio/Document/Sticker)
// implements both, verified against the library's own generated getters
// (proto/waE2E). Lets captureMediaPending read directPath/mediaKey/
// fileSHA256/fileEncSHA256/fileLength off whichever sub-message detectMedia
// already found, no second type switch over the *waE2E.Message needed.
type downloadableRef interface {
	wmeow.DownloadableMessage
	GetFileLength() uint64
}

// mediaMsg holds what we extracted from a media event proto — enough to
// download and describe the media without re-reading the event later.
type mediaMsg struct {
	proto   *waE2E.Message
	mime    string
	caption string // caption for image/video, filename for document, "" otherwise
	ref     downloadableRef
}

// detectMedia checks whether evt.Message contains supported inbound media
// (Image, Video, Audio, Document, Sticker). Returns ok=false for plain text
// and ExtendedText messages.
func detectMedia(evt *events.Message) (m mediaMsg, ok bool) {
	msg := evt.Message
	switch {
	case msg.GetImageMessage() != nil:
		im := msg.GetImageMessage()
		return mediaMsg{proto: msg, mime: im.GetMimetype(), caption: im.GetCaption(), ref: im}, true
	case msg.GetVideoMessage() != nil:
		vm := msg.GetVideoMessage()
		return mediaMsg{proto: msg, mime: vm.GetMimetype(), caption: vm.GetCaption(), ref: vm}, true
	case msg.GetAudioMessage() != nil:
		am := msg.GetAudioMessage()
		return mediaMsg{proto: msg, mime: am.GetMimetype(), ref: am}, true
	case msg.GetDocumentMessage() != nil:
		dm := msg.GetDocumentMessage()
		return mediaMsg{proto: msg, mime: dm.GetMimetype(), caption: dm.GetFileName(), ref: dm}, true
	case msg.GetStickerMessage() != nil:
		sm := msg.GetStickerMessage()
		return mediaMsg{proto: msg, mime: "image/webp", ref: sm}, true
	}
	return mediaMsg{}, false
}

// captureMediaPending persists m's raw download reference (media_pending)
// so a later worker can still fetch it after this handler's *events.Message
// — the only place carrying the protobuf — is discarded (ct-2026-07-21-1437
// parte 1). Called both when a live download fails (downloadAndStoreMedia
// below) and for every historical media message (history.go, which never
// even attempts a download).
func (a *Adapter) captureMediaPending(chatJID, msgID string, ts int64, m mediaMsg) {
	if a.store == nil {
		return
	}
	kind, ok := store.MediaKind(m.mime)
	if !ok {
		return
	}
	if err := a.store.AddMediaPending(store.MediaPending{
		ChatJID:       chatJID,
		MsgID:         msgID,
		Mime:          m.mime,
		Kind:          kind,
		DirectPath:    m.ref.GetDirectPath(),
		MediaKey:      m.ref.GetMediaKey(),
		FileSHA256:    m.ref.GetFileSHA256(),
		FileEncSHA256: m.ref.GetFileEncSHA256(),
		FileLength:    m.ref.GetFileLength(),
		TS:            ts,
	}); err != nil {
		log.Printf("whatsmeow: store media_pending msg=%s: %v", msgID, err)
	}
}

// downloadAndStoreMedia downloads m, persists the file(s), and adds a
// store.Media row. Always returns mime and caption from the proto (so
// handleMessage can set Inbound.Type/Text correctly even when the download
// fails). File/store errors are logged and swallowed — a media download
// failure must never drop the message or block the pipeline. A failed
// download instead captures the raw reference via captureMediaPending
// (ct-2026-07-21-1437 parte 1) — the only chance to ever recover the file,
// since the protobuf carrying it is gone once this call returns.
//
// The download is synchronous in the whatsmeow event handler goroutine —
// same trade-off as handleConnected's synchronous GetJoinedGroups call
// (ct-2026-07-10-0420, Amatista Area 3). For most media this is ~200ms;
// move to a goroutine if tail latency under burst load becomes a concern.
func (a *Adapter) downloadAndStoreMedia(ctx context.Context, msgID, chatJID string, ts int64, m mediaMsg) (mime, caption string) {
	mime, caption = m.mime, m.caption
	if a.mediaDir == "" || a.store == nil {
		return
	}
	data, err := a.client.DownloadAny(ctx, m.proto)
	if err != nil {
		log.Printf("whatsmeow: download media msg=%s chat=%s: %v", msgID, chatJID, err)
		a.captureMediaPending(chatJID, msgID, ts, m)
		return
	}
	path, fullPath, err := saveMedia(data, a.mediaDir, msgID, mime, a.lowQuality)
	if err != nil {
		log.Printf("whatsmeow: save media msg=%s: %v", msgID, err)
		return
	}
	if err := a.store.AddMedia(store.Media{
		MsgID:    msgID,
		ChatJID:  chatJID,
		Path:     path,
		FullPath: fullPath,
		Mime:     mime,
		Size:     int64(len(data)),
		TS:       ts,
	}); err != nil {
		log.Printf("whatsmeow: store media msg=%s: %v", msgID, err)
	}
	return
}

// mediaPendingType maps store.MediaPending's own Kind (MediaKind's output)
// to whatsmeow's MediaType — the same classification the library's own
// classToMediaType encodes per concrete sub-message (download.go), kept
// here because media_pending only stores the kind label, not the original
// proto sub-message, so downloadMediaPending has nothing to type-switch on.
func mediaPendingType(kind string) (wmeow.MediaType, bool) {
	switch kind {
	case "photo", "sticker": // StickerMessage classifies as MediaImage too (library's own classToMediaType)
		return wmeow.MediaImage, true
	case "video":
		return wmeow.MediaVideo, true
	case "audio":
		return wmeow.MediaAudio, true
	case "doc":
		return wmeow.MediaDocument, true
	}
	return "", false
}

// errMediaDownloadInFlight means (chatJID, msgID) is already being
// downloaded by the OTHER media_pending consumer (ct-2026-07-21-1437 parte
// 2's background worker vs parte 3's on-demand parallel fetch — either can
// reach the same row). Not a real failure: callers must not count it as a
// failed attempt, just move on.
var errMediaDownloadInFlight = errors.New("media download already in flight")

// isPermanentMediaDownloadError returns true if err indicates permanent
// media expiry (403 Forbidden, 410 Gone) — the URL is dead and retrying
// is wasted work. whatsmeow's own download.go already skips retries for
// these status codes exactly once, but our caller (mediabgworker) has its
// own independent retry loop of store.MaxMediaPendingAttempts; this lets it
// skip the pending row immediately instead of burning 3 attempts × pacing
// delay.
func isPermanentMediaDownloadError(err error) bool {
	return errors.Is(err, wmeow.ErrMediaDownloadFailedWith403) ||
		errors.Is(err, wmeow.ErrMediaDownloadFailedWith410)
}

// applyMediaDownloadFailure records a failed download attempt against
// (chatJID, msgID)'s media_pending row — the ONE place deciding permanent
// (403/410, give up in this single call via FailMediaPendingPermanently) vs
// transient (plain +1, IncrementMediaPendingAttempts) — shared by
// mediabgworker.go's background FIFO and mediaworker.go's on-demand burst
// so the classification can't drift between the two (ct-2026-07-29 fix: the
// on-demand path used to always +1, even on a 403 it could never recover
// from, and the background path's own "give up in 1 attempt" log was a lie
// — it also always +1'd, taking 3 real failures to actually stop retrying;
// measured on the boss's log as repeated 403s for the same msg_id).
func (a *Adapter) applyMediaDownloadFailure(chatJID, msgID string, err error) error {
	if isPermanentMediaDownloadError(err) {
		return a.store.FailMediaPendingPermanently(chatJID, msgID)
	}
	return a.store.IncrementMediaPendingAttempts(chatJID, msgID)
}

// claimMediaDownload reserves (chatJID, msgID) for the caller so the other
// media_pending consumer skips it while the download is in progress —
// returns false if it's already claimed. Every successful claim MUST be
// paired with releaseMediaDownload (downloadMediaPending does this via
// defer).
func (a *Adapter) claimMediaDownload(chatJID, msgID string) bool {
	key := chatJID + "\x00" + msgID
	_, alreadyClaimed := a.mediaInFlight.LoadOrStore(key, struct{}{})
	return !alreadyClaimed
}

// releaseMediaDownload frees a claim taken by claimMediaDownload.
func (a *Adapter) releaseMediaDownload(chatJID, msgID string) {
	a.mediaInFlight.Delete(chatJID + "\x00" + msgID)
}

// downloadMediaPending downloads p's referenced file — captured earlier by
// captureMediaPending (ct-2026-07-21-1437 parte 1), from either the live
// failure path or a historical message — straight from WhatsApp's media
// server. Uses client.DownloadMediaWithPath (the raw-fields form), not
// client.Download, since only p's own fields survive in media_pending, not
// the original protobuf sub-message. On success, persists store.Media and
// removes the pending row so it's never retried again; on failure, leaves
// the row for the caller's own retry policy (ct-2026-07-21-1437 parte 2's
// background worker, parte 3's on-demand fetch) — both share this one
// function, coordinated via claimMediaDownload so neither downloads the
// same row the other already claimed.
func (a *Adapter) downloadMediaPending(ctx context.Context, p store.MediaPending) error {
	if !a.claimMediaDownload(p.ChatJID, p.MsgID) {
		return errMediaDownloadInFlight
	}
	defer a.releaseMediaDownload(p.ChatJID, p.MsgID)

	mediaType, ok := mediaPendingType(p.Kind)
	if !ok {
		return fmt.Errorf("media_pending %s/%s: unknown kind %q", p.ChatJID, p.MsgID, p.Kind)
	}
	data, err := a.client.DownloadMediaWithPath(ctx, p.DirectPath, p.FileEncSHA256, p.FileSHA256, p.MediaKey, mediaType, "", false)
	if err != nil {
		return fmt.Errorf("download %s/%s: %w", p.ChatJID, p.MsgID, err)
	}
	path, fullPath, err := saveMedia(data, a.mediaDir, p.MsgID, p.Mime, a.lowQuality)
	if err != nil {
		return fmt.Errorf("save %s/%s: %w", p.ChatJID, p.MsgID, err)
	}
	if err := a.store.AddMedia(store.Media{
		MsgID: p.MsgID, ChatJID: p.ChatJID, Path: path, FullPath: fullPath,
		Mime: p.Mime, Size: int64(len(data)), TS: p.TS,
	}); err != nil {
		return fmt.Errorf("store %s/%s: %w", p.ChatJID, p.MsgID, err)
	}
	return a.store.DeleteMediaPending(p.ChatJID, p.MsgID)
}

// saveMedia writes the original file and, for non-webp images, a low-quality
// JPEG copy. Returns (path, fullPath): path is the low-q JPEG (what
// get_media returns — fewer tokens for the agent); fullPath is always the
// original (what get_media_full returns). For non-image media or when the
// low-q encoding fails, path == fullPath.
func saveMedia(data []byte, mediaDir, msgID, mime string, lowQuality int) (path, fullPath string, err error) {
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir %s: %w", mediaDir, err)
	}
	ext := extensionFor(mime)
	fullPath = filepath.Join(mediaDir, safeMediaName(msgID, ext))
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		return "", "", fmt.Errorf("write %s: %w", fullPath, err)
	}
	// Low-q JPEG for images — skip webp (stickers, unsupported by stdlib
	// image decoder) and fall back to fullPath when decoding fails.
	if strings.HasPrefix(mime, "image/") && mime != "image/webp" {
		lowPath := filepath.Join(mediaDir, msgID+"_low.jpg")
		if encErr := saveLowQJPEG(data, lowPath, lowQuality); encErr == nil {
			return lowPath, fullPath, nil
		}
	}
	return fullPath, fullPath, nil
}

// saveLowQJPEG decodes orig as any supported image format and re-encodes it
// as JPEG at quality. Fails fast if the format is unsupported.
func saveLowQJPEG(orig []byte, dest string, quality int) error {
	img, _, err := image.Decode(bytes.NewReader(orig))
	if err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: quality})
}

// safeMediaName returns a filename-safe base name for msgID with the given
// extension. WhatsApp message IDs can contain chars unsafe for some
// filesystems; replace the common ones with underscores.
func safeMediaName(msgID, ext string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_",
		"*", "_", "?", "_", `"`, "_",
		"<", "_", ">", "_", "|", "_",
	)
	return r.Replace(msgID) + ext
}

// extensionFor returns the conventional file extension (with leading dot) for
// a MIME type.  Covers the types WhatsApp commonly sends; unknown types get
// ".bin".
func extensionFor(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/3gpp":
		return ".3gp"
	case "audio/ogg; codecs=opus", "audio/ogg":
		return ".ogg"
	case "audio/mp4":
		return ".m4a"
	case "audio/mpeg":
		return ".mp3"
	case "application/pdf":
		return ".pdf"
	}
	// Generic: strip the type/ prefix and use what's left, or ".bin".
	if _, after, ok := strings.Cut(mime, "/"); ok && after != "" && !strings.ContainsAny(after, " ;") {
		return "." + after
	}
	return ".bin"
}
