// Catálogo de superficie de whatsmeow — qué se usa hoy, qué está listo para
// cablear cuando se pida, y qué se descartó (y por qué). Vive separado del
// conector real (adapter.go/inbound.go/outbound.go) para no mezclar "lo que
// el core efectivamente puede pedir hoy" con "lo que la lib puede hacer".
//
// Nada de este archivo se llama desde el resto del repo todavía — son
// wrappers finos, ya escritos y compilando, para que sumar una feature
// futura sea "cablear el wrapper que ya existe", no "leer los docs de
// whatsmeow de cero". Cablear = exponerlo en gateway.Gateway (si el core
// lo necesita en general) o llamarlo directo desde una tool MCP boss-only
// (si es admin, como ya hacen las 6 de grupo/perfil de openwa).
package whatsmeow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"log"

	wmeow "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	wsocket "go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/mediautil"
)

// ── USADOS (cableados en el conector, Prioridad 1) ──────────────────────
//
// - Start/Stop/Connected/QRChannel — ciclo de vida + primer login (adapter.go).
// - Inbound (events.Message → gateway.Inbound) — inbound.go.
// - Send (SendMessage texto), SetTyping (SendChatPresence), MarkRead
//   (MarkRead), MarkDelivered (no-op) — outbound.go, los 4 métodos que
//   gateway.Gateway expone al core.
// - GetJoinedGroups — llamado y logueado tras *events.Connected
//   (inbound.go: handleConnected) para confirmar que la vía sigue viva;
//   AÚN NO alimenta el store (ver el comentario en handleConnected —
//   judgment call flagueado a Citrino, no decidido acá).

// ── POR USAR (wrappers listos, un llamado cuando se pidan) ──────────────
// Visión del boss: acá van después stickers y llamadas-voz.

// React sends an emoji reaction to msgID in chatJID — reaction="" removes
// a previous reaction. Ready to expose as a Gateway method or a boss-only
// MCP tool (needs the sender JID of the ORIGINAL message, not just the
// chat — same per-message-sender gap noted in outbound.go's MarkRead).
func (a *Adapter) React(ctx context.Context, chatJID, senderJID, msgID, reaction string) error {
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return err
	}
	sender, err := types.ParseJID(senderJID)
	if err != nil {
		return err
	}
	_, err = a.client.SendMessage(ctx, chat, a.client.BuildReaction(chat, sender, types.MessageID(msgID), reaction))
	return err
}

// SendSticker uploads webp bytes and sends them as a sticker. Uses
// wmeow.MediaImage for the upload — whatsmeow has no separate "single
// sticker" MediaType (only MediaStickerPack, for pack metadata bundles, a
// different concept); stickers are WebP images uploaded the same way as a
// regular image. Unverified against a real send (Priority 2, not smoke-
// tested like the Priority 1 methods) — confirm this the first time it's
// actually wired to something.
func (a *Adapter) SendSticker(ctx context.Context, toJID string, webp []byte) (gateway.SendResult, error) {
	jid, err := types.ParseJID(toJID)
	if err != nil {
		return gateway.SendResult{}, err
	}
	up, err := a.client.Upload(ctx, webp, wmeow.MediaImage)
	if err != nil {
		return gateway.SendResult{}, err
	}
	resp, err := a.client.SendMessage(ctx, jid, &waE2E.Message{
		StickerMessage: &waE2E.StickerMessage{
			URL:           &up.URL,
			DirectPath:    &up.DirectPath,
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("image/webp"),
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    &up.FileLength,
		},
	})
	return gateway.SendResult{MsgID: string(resp.ID), TS: resp.Timestamp.Unix()}, err
}

// SendImage uploads image bytes and sends them with an optional caption.
func (a *Adapter) SendImage(ctx context.Context, toJID string, jpeg []byte, caption string) (gateway.SendResult, error) {
	jid, err := types.ParseJID(toJID)
	if err != nil {
		return gateway.SendResult{}, err
	}
	up, err := a.client.Upload(ctx, jpeg, wmeow.MediaImage)
	if err != nil {
		return gateway.SendResult{}, err
	}
	resp, err := a.client.SendMessage(ctx, jid, &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			URL:           &up.URL,
			DirectPath:    &up.DirectPath,
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("image/jpeg"),
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    &up.FileLength,
			Caption:       protoStringOrNil(caption),
		},
	})
	return gateway.SendResult{MsgID: string(resp.ID), TS: resp.Timestamp.Unix()}, err
}

// SendAudio uploads Opus bytes and sends them as a WhatsApp voice note
// (PTT: true) — T123, ct-2026-09-02-2121. data is already validated OGG/
// Opus by the time this is called (mcpserver/send.go's mediautil.IsOggOpus
// at ingestion; this never re-checks that). Mimetype is hardcoded to the
// one value WhatsApp expects for a voice note, same convention SendImage
// already uses for "image/jpeg" — never the caller's own mime string.
// seconds <= 0 leaves AudioMessage.Seconds unset (WhatsApp shows no
// duration) — never guessed from the file (decision explicit in the
// contract: no OGG parsing). Waveform prefers a sidecar the caller
// measured from the original uncompressed audio (T128 iteration 2) and
// falls back to mediautil.OpusWaveform's own approximation from opus's
// container when there isn't one — either way, never the synthetic
// placeholder T126's diagnostic waveform used before T128. Before
// upload, mediautil.NormalizeOggOpus (T131, ct-2026-09-03-0621) rewrites
// the OGG container's sample rate/pre-skip/page size — three of the four
// things WhatsApp mobile's playback needs; the fourth (the encoder's
// bitstream mode) is CleverCoder's, not this function's.
func (a *Adapter) SendAudio(ctx context.Context, toJID string, opus []byte, seconds int) (gateway.SendResult, error) {
	jid, err := types.ParseJID(toJID)
	if err != nil {
		return gateway.SendResult{}, err
	}
	// T128 iteration 2 (ct-2026-09-03-0133): a sidecar saved by
	// mcpserver.saveOutboundAudio wins when send_message's caller supplied
	// one (measured from the original uncompressed audio — better than our
	// own guess from the already-compressed Opus); missing/invalid falls
	// back to computing it here, same as before iteration 2 existed. Keyed
	// and computed off the ORIGINAL opus bytes, before T131's container
	// normalization below — the audio packets never change, so either form
	// yields the same waveform, but this ordering means a reader never has
	// to verify that for themselves.
	waveform, ok := mediautil.LoadWaveformSidecar(a.mediaDir, opus)
	if !ok {
		waveform = mediautil.OpusWaveform(opus)
	}
	// T131 (ct-2026-09-03-0621): normalize the container (sample rate,
	// pre-skip, page size) right before upload — never fails, a file it
	// can't safely rewrite goes out exactly as it came in.
	opus = mediautil.NormalizeOggOpus(opus)
	up, err := a.client.Upload(ctx, opus, wmeow.MediaAudio)
	if err != nil {
		return gateway.SendResult{}, err
	}
	msg := &waE2E.AudioMessage{
		URL:           &up.URL,
		DirectPath:    &up.DirectPath,
		MediaKey:      up.MediaKey,
		Mimetype:      proto.String(oggOpusMime),
		FileEncSHA256: up.FileEncSHA256,
		FileSHA256:    up.FileSHA256,
		FileLength:    &up.FileLength,
		PTT:           proto.Bool(true),
		Waveform:      waveform,
	}
	if seconds > 0 {
		msg.Seconds = proto.Uint32(uint32(seconds))
	}
	resp, err := a.client.SendMessage(ctx, jid, &waE2E.Message{AudioMessage: msg})
	return gateway.SendResult{MsgID: string(resp.ID), TS: resp.Timestamp.Unix()}, err
}

// oggOpusMime is the exact mime WhatsApp expects for a voice note — the
// ONLY value SendAudio ever writes to AudioMessage.Mimetype, same
// convention SendImage already uses hardcoding "image/jpeg".
const oggOpusMime = "audio/ogg; codecs=opus"

// SendMedia implements gateway.Gateway's media seam (T122, ct-2026-09-02-
// 2045) — ONE method, dispatched by media.Kind, so a new media type
// (T123's "audio") is a new case here, not a new gateway.Gateway method.
// "photo" reuses SendImage verbatim (data is already a validated JPEG by
// the time this is called — mcpserver/send.go runs mediautil.DecodeDataURL
// + EnsureJPEG at ingestion, this never re-derives that). media.Mime is
// accepted for interface symmetry with the caller's stored record but
// unused here: both SendImage and SendAudio hardcode their own canonical
// mime, never the caller's string.
func (a *Adapter) SendMedia(ctx context.Context, toJID string, media gateway.OutboundMedia) (gateway.SendResult, error) {
	switch media.Kind {
	case "photo":
		return a.SendImage(ctx, toJID, media.Data, media.Caption)
	case "audio":
		return a.SendAudio(ctx, toJID, media.Data, media.Seconds)
	default:
		return gateway.SendResult{}, fmt.Errorf("whatsmeow: unsupported media kind %q", media.Kind)
	}
}

// DownloadMedia decrypts and downloads the media in a received message —
// pair with events.Message.Message (the raw proto) from Inbound's source
// event, which piumy-gateway's gateway.Inbound doesn't carry today (F4d's
// media-by-path model was designed against open-wa's webhook, not this).
func (a *Adapter) DownloadMedia(ctx context.Context, msg *waE2E.Message) ([]byte, error) {
	return a.client.DownloadAny(ctx, msg)
}

// EditMessage / DeleteMessage: whatsmeow already exposes these as direct,
// single-call client methods — no wrapper needed, just call them with a
// parsed JID:
//
//	client.SendMessage(ctx, chat, client.BuildEdit(chat, msgID, newContent))
//	client.RevokeMessage(ctx, chat, msgID)

// IsOnWhatsApp checks whether phone numbers (no JID suffix, just digits)
// have WhatsApp accounts — useful before a first outbound to a number
// nobody has messaged yet.
func (a *Adapter) IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	return a.client.IsOnWhatsApp(ctx, phones)
}

// Group/profile admin — cabled into mcpserver/group_tools.go (ST-E,
// ct-2026-07-11-1444) via the GroupProfile interface (mcpserver/
// server.go's Deps.GroupProfile — *Adapter satisfies it). Boss-only,
// direct client calls, no gateway.Gateway seam (group admin isn't a
// cross-vendor concept the core needs to know about — a future adapter
// could handle groups completely differently).
func (a *Adapter) CreateGroup(ctx context.Context, name string, participantJIDs []string) (*types.GroupInfo, error) {
	participants := make([]types.JID, 0, len(participantJIDs))
	for _, p := range participantJIDs {
		jid, err := types.ParseJID(p)
		if err != nil {
			return nil, err
		}
		participants = append(participants, jid)
	}
	return a.client.CreateGroup(ctx, wmeow.ReqCreateGroup{Name: name, Participants: participants})
}

func (a *Adapter) AddParticipant(ctx context.Context, groupJID, participantJID string) ([]types.GroupParticipant, error) {
	group, err := types.ParseJID(groupJID)
	if err != nil {
		return nil, err
	}
	participant, err := types.ParseJID(participantJID)
	if err != nil {
		return nil, err
	}
	return a.client.UpdateGroupParticipants(ctx, group, []types.JID{participant}, wmeow.ParticipantChangeAdd)
}

// PromoteParticipants grants group-admin rights (T135, ct-2026-09-03-1546)
// — whatsmeow already exposes this via UpdateGroupParticipants +
// ParticipantChangePromote, Piumy just never called it. Takes several JIDs
// in one request (not one call per JID like AddParticipant) since
// create_group's own caller can have more than one is_boss match to
// promote at once — the underlying WhatsApp op is already batched, no
// reason to serialize it here.
func (a *Adapter) PromoteParticipants(ctx context.Context, groupJID string, participantJIDs []string) ([]types.GroupParticipant, error) {
	group, err := types.ParseJID(groupJID)
	if err != nil {
		return nil, err
	}
	participants := make([]types.JID, 0, len(participantJIDs))
	for _, p := range participantJIDs {
		jid, err := types.ParseJID(p)
		if err != nil {
			return nil, err
		}
		participants = append(participants, jid)
	}
	return a.client.UpdateGroupParticipants(ctx, group, participants, wmeow.ParticipantChangePromote)
}

// profilePhotoMaxBytes bounds the payload SetGroupPhoto embeds directly in
// the IQ stanza (T111, ct-2026-09-01-1442) — unlike SendImage, which
// uploads to a CDN first and sends only a reference, this call carries the
// raw bytes inline, so it's bound by whatsmeow's own socket frame ceiling
// (wsocket.FrameMaxSize) before a single byte reaches the network. This is
// NOT WhatsApp's server-side limit for a profile/group photo specifically
// — that's unmeasured, and there is no safe way to measure it against a
// live account (Citrino, T111: "no vale el dato"). It's the one ceiling
// this codebase can verify with certainty.
const profilePhotoMaxBytes = wsocket.FrameMaxSize

// checkPhotoSize rejects a payload too large to even attempt sending — nil
// (SetGroupPhoto's own "remove the photo" signal) always passes.
func checkPhotoSize(jpeg []byte) error {
	if len(jpeg) > profilePhotoMaxBytes {
		return fmt.Errorf("la imagen es demasiado grande (%d bytes, máximo %d) — prueba con una imagen más chica", len(jpeg), profilePhotoMaxBytes)
	}
	return nil
}

// wrapSetPhotoError rewrites wmeow.ErrInvalidImageFormat into an honest
// message (T111, ct-2026-09-01-1442, Citrino's own finding): whatsmeow maps
// EVERY server rejection (IQ 406 "not-acceptable") to this same error,
// format-invalid or not — group.go:346-347 in the vendored library. Telling
// the dueño "invalid format" when the real cause might be size or
// dimensions is the same shape of misdirection T104/T87 already fixed
// elsewhere in this codebase ("un error que manda a buscar en la dirección
// equivocada") — inherited from the library here, not fixable there, but
// not worth repeating verbatim either. Names BOTH possible causes, honestly
// — never guesses which one it actually was.
//
// Also logs the payload's real size and decoded dimensions (best-effort;
// DecodeConfig failing just means "dimensiones desconocidas", never a
// reason to skip the log) — the data this codebase doesn't have today about
// where WhatsApp's real server-side limit sits, captured for free the first
// time a real rejection happens, instead of provoking one on purpose
// against the live account. Same idea as piumy.log's own reason to exist:
// "este archivo existe para pedírselo a un usuario cuando algo no le llega".
func wrapSetPhotoError(err error, jpeg []byte) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, wmeow.ErrInvalidImageFormat) {
		return err
	}
	dims := "dimensiones desconocidas"
	if cfg, _, cfgErr := image.DecodeConfig(bytes.NewReader(jpeg)); cfgErr == nil {
		dims = fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
	}
	log.Printf("whatsmeow: WhatsApp rechazó la imagen (%d bytes, %s) — formato o tamaño, el servidor no distingue: %v", len(jpeg), dims, err)
	return fmt.Errorf("WhatsApp rechazó la imagen. Puede ser el formato o el tamaño — el servidor no distingue entre los dos. Prueba con una imagen más chica")
}

func (a *Adapter) SetGroupPhoto(ctx context.Context, groupJID string, jpeg []byte) (string, error) {
	if err := checkPhotoSize(jpeg); err != nil {
		return "", fmt.Errorf("whatsmeow: %w", err)
	}
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return "", err
	}
	id, err := a.client.SetGroupPhoto(ctx, jid, jpeg)
	if err != nil {
		return "", wrapSetPhotoError(err, jpeg)
	}
	return id, nil
}

// SetProfilePhoto changes the host number's OWN profile photo (T111,
// ct-2026-09-01-1442) — SetGroupPhoto's underlying IQ isn't group-specific
// despite its name (verified against the vendored library before coding):
// the recipient is just a parameter, and the own JID makes it a profile
// photo instead of a group icon. Reuses SetGroupPhoto verbatim, passing the
// own JID as the string it already expects — no separate ParseJID, no
// duplicated size/error handling. nil jpeg removes the photo (whatsmeow's
// own contract, free with the same call).
func (a *Adapter) SetProfilePhoto(ctx context.Context, jpeg []byte) (string, error) {
	if a.client.Store.ID == nil {
		return "", fmt.Errorf("whatsmeow: no logueado, sin JID propio")
	}
	return a.SetGroupPhoto(ctx, a.client.Store.ID.ToNonAD().String(), jpeg)
}

func (a *Adapter) SetGroupDescription(ctx context.Context, groupJID, description string) error {
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return err
	}
	return a.client.SetGroupDescription(ctx, jid, description)
}

// SetProfileStatus sets the host number's own status/"About" text (boss's
// decision A, ct-2026-07-11-1444) — NOT the display name, whatsmeow has no
// API for that. Wraps SetStatusMessage 1:1, same wrapper shape as the group
// methods above.
func (a *Adapter) SetProfileStatus(ctx context.Context, status string) error {
	return a.client.SetStatusMessage(ctx, types.SetStatusInput{Text: &status})
}

// GetProfileStatus reads the host number's own status/"About" text LIVE
// from WhatsApp (T96, ct-2026-08-28-1743 — the read half of
// SetProfileStatus/T92, so the dashboard can show it under the name and
// pre-fill the edit field instead of showing it blank over an existing
// value). GetUserInfo is a usync query (the same call the dashboard's
// Contactos/Números tabs already trigger indirectly for other JIDs); own
// JID is a.client.Store.ID, nil before pairing. An unset status is a
// legitimate, common WhatsApp state (most accounts never set one) — this
// returns "" for that, not an error; info[own] simply being the zero
// UserInfo if the server's usync response omits our own entry for any
// reason has the same effect, deliberately not special-cased.
func (a *Adapter) GetProfileStatus(ctx context.Context) (string, error) {
	if a.client.Store.ID == nil {
		return "", fmt.Errorf("whatsmeow: no logueado, sin JID propio")
	}
	own := *a.client.Store.ID
	info, err := a.client.GetUserInfo(ctx, []types.JID{own})
	if err != nil {
		return "", err
	}
	return info[own].Status, nil
}

// ── NO USAR (evaluado y descartado, con motivo) ─────────────────────────
//
// - Newsletters (NewsletterSendReaction, UploadNewsletter*, etc.) —
//   piumy-gateway media un WhatsApp personal/de dueño, no un canal de
//   difusión masiva; fuera de la visión del producto.
// - Calls (no hay API de voz/video en whatsmeow — es texto/media, no
//   señalización de llamada) — el boss mencionó llamadas-voz como visión
//   futura, pero eso NO es esta librería; sería un componente aparte.
// - Polls — no hay pedido del boss ni caso de uso del gateway hoy.
// - FB downloads (DownloadFB/DownloadFBToFile) — mecanismo interno para
//   media servida desde infraestructura de Facebook/Meta específica
//   (ciertos stickers/GIFs), no aplica al camino normal de media.
// - Proxy (client.SetProxy y afines) — piumy-gateway corre en la red del
//   dueño; no hay caso de uso de proxy todavía, YAGNI hasta que aparezca.
// - Sticker-packs (creación/publicación de packs, distinto de ENVIAR un
//   sticker suelto, que sí está en "por usar") — gestión de contenido, no
//   parte de ser un gateway de mensajería.

// protoStringOrNil is proto.String, but "" means "field absent" (an empty
// caption should omit ImageMessage.Caption entirely, not send an empty
// string) — the one bit proto.String itself doesn't cover.
func protoStringOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}
