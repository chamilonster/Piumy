package store

import (
	"database/sql"
	"errors"
	"time"
)

// UsageWeights are the calibration knobs for the token estimate (F4-DESIGN
// §8) — always sourced from config, never hardcoded here.
type UsageWeights struct {
	OutCharWeight float64
	InCharWeight  float64
	ImageCost     float64
	AudioCost     float64
	MessageCost   float64
}

// Usage is one chat's raw counters for one day — the source of truth. The
// estimate/blend are computed on READ (EstimateTokens/BlendUsage), never
// stored precomputed, so recalibrating weights later never requires
// touching historical rows.
type Usage struct {
	ChatJID    string
	Day        string
	OutChars   int
	InChars    int
	Images     int
	Audio      int
	Messages   int
	TokensReal float64
}

// UsageDelta is what AddUsage increments — a zero field is a no-op for that
// counter, so a caller only fills in what it's actually recording.
type UsageDelta struct {
	OutChars   int
	InChars    int
	Images     int
	Audio      int
	Messages   int
	TokensReal float64
}

// Today returns the UTC day bucket ("YYYY-MM-DD") AddUsage/TotalUsageToday
// key on.
func Today() string {
	return time.Now().UTC().Format("2006-01-02")
}

// AddUsage increments chatJID's counters for day by delta (upsert).
func (s *Store) AddUsage(chatJID, day string, delta UsageDelta) error {
	_, err := s.db.Exec(`INSERT INTO usage (chat_jid, day, out_chars, in_chars, images, audio, messages, tokens_real)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_jid, day) DO UPDATE SET
			out_chars = out_chars + excluded.out_chars,
			in_chars = in_chars + excluded.in_chars,
			images = images + excluded.images,
			audio = audio + excluded.audio,
			messages = messages + excluded.messages,
			tokens_real = tokens_real + excluded.tokens_real`,
		chatJID, day, delta.OutChars, delta.InChars, delta.Images, delta.Audio, delta.Messages, delta.TokensReal)
	return err
}

// UsageForDay returns chatJID's counters for day, or a zero Usage if
// nothing's been recorded yet (not an error — an unmetered chat is normal).
func (s *Store) UsageForDay(chatJID, day string) (Usage, error) {
	u := Usage{ChatJID: chatJID, Day: day}
	row := s.db.QueryRow(`SELECT out_chars, in_chars, images, audio, messages, tokens_real
		FROM usage WHERE chat_jid = ? AND day = ?`, chatJID, day)
	if err := row.Scan(&u.OutChars, &u.InChars, &u.Images, &u.Audio, &u.Messages, &u.TokensReal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, nil
		}
		return Usage{}, err
	}
	return u, nil
}

// EstimateTokens is the char/media-based estimate (F4-DESIGN §8):
// est ≈ out_chars/4·OutCharWeight + in_chars/4·InCharWeight +
// images·ImageCost + audio·AudioCost + messages·MessageCost.
func EstimateTokens(u Usage, w UsageWeights) float64 {
	return float64(u.OutChars)/4*w.OutCharWeight +
		float64(u.InChars)/4*w.InCharWeight +
		float64(u.Images)*w.ImageCost +
		float64(u.Audio)*w.AudioCost +
		float64(u.Messages)*w.MessageCost
}

// BlendUsage is the 70/30 blend: real reported tokens weighted 0.7, the
// char/media estimate weighted 0.3 — falls back to the pure estimate when
// nothing's been reported yet. This fallback IS the token-report seam's
// "runs without the real report" behavior (F4-DESIGN §9) — no separate stub
// component needed, it falls out of the blend formula itself.
func BlendUsage(u Usage, w UsageWeights) float64 {
	est := EstimateTokens(u, w)
	if u.TokensReal <= 0 {
		return est
	}
	return 0.7*u.TokensReal + 0.3*est
}

// TotalUsageToday sums the BLENDED usage across every chat for today — the
// global figure capipush's quota check reads (single-account for now,
// F4-DESIGN §8: "cuota de cuenta... global simple por ahora").
func (s *Store) TotalUsageToday(w UsageWeights) (float64, error) {
	rows, err := s.db.Query(`SELECT out_chars, in_chars, images, audio, messages, tokens_real
		FROM usage WHERE day = ?`, Today())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total float64
	for rows.Next() {
		var u Usage
		if err := rows.Scan(&u.OutChars, &u.InChars, &u.Images, &u.Audio, &u.Messages, &u.TokensReal); err != nil {
			return 0, err
		}
		total += BlendUsage(u, w)
	}
	return total, rows.Err()
}
