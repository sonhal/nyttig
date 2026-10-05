package clef

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoad_Sample keeps deploy/clef.sample.toml in sync with Config.
func TestLoad_Sample(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "deploy", "clef.sample.toml"))
	if err != nil {
		t.Fatalf("Load(clef.sample.toml): %v", err)
	}
	if cfg.View != "clef-inbox" || cfg.Model != "clef-flash" || cfg.IntervalDuration != time.Minute {
		t.Errorf("cfg = %+v", cfg)
	}
	if len(cfg.Questions) != 2 || cfg.Questions[0].Noul == nil || len(cfg.Questions[1].Levels) != 5 {
		t.Errorf("questions = %+v", cfg.Questions)
	}
	if cfg.MaxDailyTokens() != 2000000 {
		t.Errorf("daily tokens = %d", cfg.MaxDailyTokens())
	}
}

const validHead = `
view = "v"
account_id = "0123abcd"
`

const noulQ = `
[[questions]]
type = "noul"
instructions = "Relevant?"
`

func loadString(t *testing.T, s string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "clef.toml")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := loadString(t, validHead+noulQ)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Socket != DefaultSocket || cfg.Assessor != "clef" || cfg.Model != "clef-flash" ||
		cfg.IntervalDuration != DefaultInterval || cfg.MaxPerMinute != 60 || cfg.MaxTextChars != 8000 ||
		cfg.MaxDailyTokens() != DefaultDailyTokens || cfg.Color != DefaultColor {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestLoad_DailyTokensZeroMeansNoLimit(t *testing.T) {
	cfg, err := loadString(t, validHead+"daily_tokens = 0\n"+noulQ)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxDailyTokens() != 0 {
		t.Errorf("daily tokens = %d, want 0", cfg.MaxDailyTokens())
	}
}

func TestLoad_Invalid(t *testing.T) {
	scoreQ := func(criteria string) string {
		return "\n[[questions]]\ntype = \"score\"\ninstructions = \"S?\"\ncriteria = " + criteria + "\n"
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{"no view", `account_id = "ab"` + noulQ, "view is required"},
		{"no account", `view = "v"` + noulQ, "account_id is required"},
		{"account not hex", `view = "v"` + "\naccount_id = \"xyz\"" + noulQ, "hexadecimal"},
		{"bad model", validHead + "model = \"gpt\"" + noulQ, "model must be"},
		{"token key", validHead + "token = \"abc\"" + noulQ, "token key is not allowed"},
		{"unknown key", validHead + "bogus = 1" + noulQ, "unknown config keys"},
		{"bad interval", validHead + "interval = \"soon\"" + noulQ, "interval"},
		{"zero interval", validHead + "interval = \"0s\"" + noulQ, "interval must be positive"},
		{"negative interval", validHead + "interval = \"-5s\"" + noulQ, "interval must be positive"},
		{"zero rate", validHead + "max_per_minute = 0" + noulQ, "max_per_minute must be positive"},
		{"negative rate", validHead + "max_per_minute = -1" + noulQ, "max_per_minute must be positive"},
		{"zero chars", validHead + "max_text_chars = 0" + noulQ, "max_text_chars must be positive"},
		{"negative tokens", validHead + "daily_tokens = -1" + noulQ, "daily_tokens"},
		{"partial tls", validHead + "[tls]\ncert = \"c\"\n" + noulQ, "cert, key and ca"},
		{"no questions", validHead, "need 1 to 64"},
		{"bad type", validHead + "\n[[questions]]\ntype = \"choice\"\ninstructions = \"x\"\n", `type must be "noul" or "score"`},
		{"no instructions", validHead + "\n[[questions]]\ntype = \"noul\"\n", "instructions are required"},
		{"long instructions", validHead + "\n[[questions]]\ntype = \"noul\"\ninstructions = \"" + strings.Repeat("a", 2001) + "\"\n", "over 2000"},
		{"noul criteria array", validHead + "\n[[questions]]\ntype = \"noul\"\ninstructions = \"x\"\ncriteria = [\"a\"]\n", "criteria is a table"},
		{"noul criteria missing false", validHead + "\n[[questions]]\ntype = \"noul\"\ninstructions = \"x\"\ncriteria = { true = \"a\" }\n", "both true and false"},
		{"noul criteria unknown key", validHead + "\n[[questions]]\ntype = \"noul\"\ninstructions = \"x\"\ncriteria = { true = \"a\", false = \"b\", maybe = \"c\" }\n", "unknown key"},
		{"score without criteria", validHead + "\n[[questions]]\ntype = \"score\"\ninstructions = \"x\"\n", "array of 2 to 10"},
		{"score one level", validHead + scoreQ(`["a"]`), "2 to 10 levels, got 1"},
		{"score eleven levels", validHead + scoreQ(`["a","b","c","d","e","f","g","h","i","j","k"]`), "2 to 10 levels, got 11"},
		{"score empty level", validHead + scoreQ(`["a",""]`), "non-empty"},
		{"score non-string level", validHead + scoreQ(`["a",2]`), "non-empty string"},
		{"score object criteria", validHead + scoreQ(`{ true = "a", false = "b" }`), "array of 2 to 10"},
		{"two whole-item questions", validHead + noulQ + noulQ, "more than one question without a tag"},
		{"two for one tag", validHead + noulQ + "\n[[questions]]\ntag = \"CVE\"\ntype = \"noul\"\ninstructions = \"x\"\n\n[[questions]]\ntag = \"cve\"\ntype = \"noul\"\ninstructions = \"y\"\n", `more than one question for tag "cve"`},
		{"missing file", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "missing file" {
				if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
					t.Fatal("want an error")
				}
				return
			}
			_, err := loadString(t, tc.body)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestLoad_QuestionLimit(t *testing.T) {
	body := validHead
	for i := 0; i < 65; i++ {
		body += "\n[[questions]]\ntag = \"t" + strings.Repeat("x", i) + "\"\ntype = \"noul\"\ninstructions = \"x\"\n"
	}
	if _, err := loadString(t, body); err == nil || !strings.Contains(err.Error(), "need 1 to 64") {
		t.Fatalf("err = %v", err)
	}
}
