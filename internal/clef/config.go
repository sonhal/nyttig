package clef

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// ── Configuration ──

// Defaults for keys the file may leave out.
const (
	DefaultSocket       = "/run/nyttig/nyttig.sock"
	DefaultAssessor     = "clef"
	DefaultDescription  = "Clef: relevance and severity, 0 to 1"
	DefaultColor        = "#F38020"
	DefaultModel        = "clef-flash"
	DefaultInterval     = 60 * time.Second
	DefaultMaxPerMinute = 60
	DefaultDailyTokens  = 2_000_000
	DefaultMaxTextChars = 8000
)

// Limits on what a config may ask for.
const (
	maxQuestions    = 64
	maxInstructions = 2000
	maxCriteriaLen  = 500
	minLevels       = 2
	maxLevels       = 10
)

var accountIDRe = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// Config mirrors nyttig-clef's TOML file (deploy/clef.sample.toml).
type Config struct {
	Socket      string `toml:"socket"`
	TLS         TLS    `toml:"tls"`
	Assessor    string `toml:"assessor"`
	Description string `toml:"description"`
	Color       string `toml:"color"`
	View        string `toml:"view"`

	AccountID string `toml:"account_id"`
	Model     string `toml:"model"`
	TokenFile string `toml:"token_file"`

	Interval     string         `toml:"interval"`
	MaxPerMinute int            `toml:"max_per_minute"`
	DailyTokens  *int           `toml:"daily_tokens"` // 0 = no limit; unset = DefaultDailyTokens
	MaxTextChars int            `toml:"max_text_chars"`
	Questions    []QuestionSpec `toml:"questions"`

	// IntervalDuration is Interval parsed (set by Load).
	IntervalDuration time.Duration `toml:"-"`
}

// TLS is the client side of mutual TLS for a remote daemon, like the TUI's
// --tls-* flags. Leave the table out for the Unix socket.
type TLS struct {
	Cert       string `toml:"cert"`
	Key        string `toml:"key"`
	CA         string `toml:"ca"`
	ServerName string `toml:"server_name"`
}

// QuestionSpec is one [[questions]] entry. Criteria is a table with the keys
// true and false (noul) or an array of levels, lowest first (score).
type QuestionSpec struct {
	Tag          string `toml:"tag"` // empty: the item as a whole
	Type         string `toml:"type"`
	Instructions string `toml:"instructions"`
	Criteria     any    `toml:"criteria"`

	// Parsed from Criteria by Load.
	Noul   *NoulCriteria `toml:"-"`
	Levels []string      `toml:"-"`
}

// Load reads, defaults and validates the config file at path. Unknown keys
// are an error, and so is a "token" key: the API token comes from token_file
// or $CLOUDFLARE_API_TOKEN, never from a file that gets copied around.
func Load(path string) (*Config, error) {
	var cfg Config
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if meta.IsDefined("token") {
		return nil, fmt.Errorf("%s: a token key is not allowed; use token_file or $CLOUDFLARE_API_TOKEN", path)
	}
	var unknown []string
	for _, k := range meta.Undecoded() {
		// criteria is decoded into an untyped value, so its members show up
		// as undecoded; their shape is checked in finish.
		if !strings.HasPrefix(k.String(), "questions.criteria.") {
			unknown = append(unknown, k.String())
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown config keys in %s: %v", path, unknown)
	}
	// An explicit zero is a mistake, not a request for the default.
	for key, v := range map[string]int{"max_per_minute": cfg.MaxPerMinute, "max_text_chars": cfg.MaxTextChars} {
		if meta.IsDefined(key) && v <= 0 {
			return nil, fmt.Errorf("%s: %s must be positive", path, key)
		}
	}
	if err := cfg.finish(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

// MaxDailyTokens is the daily input-token budget; 0 means no limit.
func (c *Config) MaxDailyTokens() int {
	if c.DailyTokens == nil {
		return DefaultDailyTokens
	}
	return *c.DailyTokens
}

func (c *Config) finish() error {
	if c.Socket == "" {
		c.Socket = DefaultSocket
	}
	if c.Assessor == "" {
		c.Assessor = DefaultAssessor
	}
	if c.Description == "" {
		c.Description = DefaultDescription
	}
	if c.Color == "" {
		c.Color = DefaultColor
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.MaxPerMinute == 0 {
		c.MaxPerMinute = DefaultMaxPerMinute
	}
	if c.MaxTextChars == 0 {
		c.MaxTextChars = DefaultMaxTextChars
	}
	c.IntervalDuration = DefaultInterval
	if c.Interval != "" {
		d, err := time.ParseDuration(c.Interval)
		if err != nil {
			return fmt.Errorf("interval: %w", err)
		}
		c.IntervalDuration = d
	}

	switch {
	case c.View == "":
		return errors.New("view is required (the saved view that finds the items to score)")
	case c.Model != "clef" && c.Model != "clef-flash":
		return fmt.Errorf(`model must be "clef-flash" or "clef", got %q`, c.Model)
	case c.AccountID == "":
		return errors.New("account_id is required")
	case !accountIDRe.MatchString(c.AccountID):
		return errors.New("account_id must be hexadecimal")
	case c.IntervalDuration <= 0:
		return errors.New("interval must be positive")
	case c.MaxPerMinute < 0:
		return errors.New("max_per_minute must be positive")
	case c.MaxTextChars < 0:
		return errors.New("max_text_chars must be positive")
	case c.DailyTokens != nil && *c.DailyTokens < 0:
		return errors.New("daily_tokens must be 0 (no limit) or positive")
	}
	if tls := c.TLS; (tls.Cert != "" || tls.Key != "" || tls.CA != "") && (tls.Cert == "" || tls.Key == "" || tls.CA == "") {
		return errors.New("[tls] needs cert, key and ca together")
	}
	return c.finishQuestions()
}

func (c *Config) finishQuestions() error {
	if n := len(c.Questions); n < 1 || n > maxQuestions {
		return fmt.Errorf("need 1 to %d [[questions]], got %d", maxQuestions, n)
	}
	seen := map[string]bool{}
	for i := range c.Questions {
		q := &c.Questions[i]
		if err := q.finish(); err != nil {
			return fmt.Errorf("questions[%d]: %w", i, err)
		}
		key := strings.ToLower(q.Tag)
		if seen[key] {
			if q.Tag == "" {
				return fmt.Errorf("questions[%d]: more than one question without a tag", i)
			}
			return fmt.Errorf("questions[%d]: more than one question for tag %q", i, q.Tag)
		}
		seen[key] = true
	}
	return nil
}

func (q *QuestionSpec) finish() error {
	if q.Instructions == "" {
		return errors.New("instructions are required")
	}
	if utf8.RuneCountInString(q.Instructions) > maxInstructions {
		return fmt.Errorf("instructions are over %d characters", maxInstructions)
	}
	switch q.Type {
	case TypeNoul:
		return q.finishNoul()
	case TypeScore:
		return q.finishScore()
	default:
		return fmt.Errorf(`type must be "noul" or "score", got %q`, q.Type)
	}
}

func (q *QuestionSpec) finishNoul() error {
	if q.Criteria == nil {
		return nil
	}
	m, ok := q.Criteria.(map[string]any)
	if !ok {
		return errors.New("a noul question's criteria is a table with true and false")
	}
	for k := range m {
		if k != "true" && k != "false" {
			return fmt.Errorf("noul criteria: unknown key %q (only true and false)", k)
		}
	}
	t, tok := m["true"].(string)
	f, fok := m["false"].(string)
	if !tok || !fok || t == "" || f == "" {
		return errors.New("noul criteria needs both true and false as non-empty strings")
	}
	if utf8.RuneCountInString(t) > maxCriteriaLen || utf8.RuneCountInString(f) > maxCriteriaLen {
		return fmt.Errorf("noul criteria are over %d characters", maxCriteriaLen)
	}
	q.Noul = &NoulCriteria{True: t, False: f}
	return nil
}

func (q *QuestionSpec) finishScore() error {
	list, ok := q.Criteria.([]any)
	if !ok {
		return fmt.Errorf("a score question's criteria is an array of %d to %d levels, lowest first", minLevels, maxLevels)
	}
	if len(list) < minLevels || len(list) > maxLevels {
		return fmt.Errorf("a score question needs %d to %d levels, got %d", minLevels, maxLevels, len(list))
	}
	levels := make([]string, len(list))
	for i, v := range list {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return fmt.Errorf("score level %d is not a non-empty string", i)
		}
		if utf8.RuneCountInString(s) > maxCriteriaLen {
			return fmt.Errorf("score level %d is over %d characters", i, maxCriteriaLen)
		}
		levels[i] = s
	}
	q.Levels = levels
	return nil
}

// ── Token ──

// ReadToken returns the Cloudflare API token: the contents of token_file when
// set (surrounding whitespace removed), else $CLOUDFLARE_API_TOKEN. The token
// is never part of the config file itself.
func (c *Config) ReadToken(readFile func(string) ([]byte, error), getenv func(string) string) (string, error) {
	var tok string
	if c.TokenFile != "" {
		b, err := readFile(c.TokenFile)
		if err != nil {
			return "", fmt.Errorf("token_file: %w", err)
		}
		tok = strings.TrimSpace(string(b))
		if tok == "" {
			return "", fmt.Errorf("token_file %s is empty", c.TokenFile)
		}
		return tok, nil
	}
	tok = strings.TrimSpace(getenv("CLOUDFLARE_API_TOKEN"))
	if tok == "" {
		return "", errors.New("no API token: set token_file or $CLOUDFLARE_API_TOKEN")
	}
	return tok, nil
}
