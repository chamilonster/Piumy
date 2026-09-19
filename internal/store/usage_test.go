package store

import "testing"

func TestAddUsageAccumulatesAndUsageForDayReads(t *testing.T) {
	s := openTestStore(t)
	day := "2026-01-01"
	if err := s.AddUsage("1@c.us", day, UsageDelta{OutChars: 100, Messages: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage("1@c.us", day, UsageDelta{OutChars: 50, InChars: 40}); err != nil {
		t.Fatal(err)
	}
	u, err := s.UsageForDay("1@c.us", day)
	if err != nil {
		t.Fatal(err)
	}
	if u.OutChars != 150 || u.InChars != 40 || u.Messages != 1 {
		t.Errorf("UsageForDay = %+v, want out_chars=150 in_chars=40 messages=1", u)
	}
}

func TestUsageForDayZeroValueForUnmeteredChat(t *testing.T) {
	s := openTestStore(t)
	u, err := s.UsageForDay("nobody@c.us", "2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if u.OutChars != 0 || u.TokensReal != 0 {
		t.Errorf("UsageForDay for an unmetered chat = %+v, want all zero", u)
	}
}

func TestEstimateTokensFormula(t *testing.T) {
	w := UsageWeights{OutCharWeight: 1, InCharWeight: 0.25, ImageCost: 800, AudioCost: 400, MessageCost: 5}
	u := Usage{OutChars: 400, InChars: 400, Images: 1, Audio: 1, Messages: 1}
	// 400/4*1 + 400/4*0.25 + 1*800 + 1*400 + 1*5 = 100 + 25 + 800 + 400 + 5 = 1330
	got := EstimateTokens(u, w)
	if got != 1330 {
		t.Errorf("EstimateTokens = %v, want 1330", got)
	}
}

func TestBlendUsageFallsBackToEstimateWithoutRealTokens(t *testing.T) {
	w := UsageWeights{OutCharWeight: 1, InCharWeight: 1, MessageCost: 1}
	u := Usage{Messages: 10} // est = 10
	if got := BlendUsage(u, w); got != 10 {
		t.Errorf("BlendUsage with TokensReal=0 = %v, want the pure estimate (10)", got)
	}
}

func TestBlendUsageWeights70_30WithRealTokens(t *testing.T) {
	w := UsageWeights{MessageCost: 1}
	u := Usage{Messages: 10, TokensReal: 1000} // est = 10, blend = 0.7*1000 + 0.3*10 = 703
	got := BlendUsage(u, w)
	if got != 703 {
		t.Errorf("BlendUsage with real tokens = %v, want 703 (0.7*1000+0.3*10)", got)
	}
}

func TestTotalUsageTodaySumsAcrossChats(t *testing.T) {
	s := openTestStore(t)
	day := Today()
	w := UsageWeights{MessageCost: 1}
	if err := s.AddUsage("1@c.us", day, UsageDelta{Messages: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage("2@c.us", day, UsageDelta{Messages: 4}); err != nil {
		t.Fatal(err)
	}
	// A different day must not count towards today's total.
	if err := s.AddUsage("3@c.us", "2000-01-01", UsageDelta{Messages: 100}); err != nil {
		t.Fatal(err)
	}
	total, err := s.TotalUsageToday(w)
	if err != nil {
		t.Fatal(err)
	}
	if total != 7 {
		t.Errorf("TotalUsageToday = %v, want 7 (3+4, excluding the other day)", total)
	}
}
