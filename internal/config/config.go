package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/models"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Searches    []SearchConfig    `yaml:"searches"`
	Marktplaats MarktplaatsConfig `yaml:"marktplaats"`
	Discord     DiscordConfig     `yaml:"discord"`
	Messenger   MessengerConfig   `yaml:"messenger"`
	Scoring     ScoringConfig     `yaml:"scoring"`
	AI          AIConfig          `yaml:"ai"`
}

type SearchConfig struct {
	Name            string            `yaml:"name"`
	Query           string            `yaml:"query"`
	MarketplaceID   string            `yaml:"marketplace_id"`
	ProfileID       int64             `yaml:"profile_id"`
	CategoryID      int               `yaml:"category_id"`
	MaxPrice        int               `yaml:"max_price"`
	MinPrice        int               `yaml:"min_price"`
	Condition       []string          `yaml:"condition"`
	OfferPercentage int               `yaml:"offer_percentage"`
	AutoMessage     bool              `yaml:"auto_message"`
	MessageTemplate string            `yaml:"message_template"`
	Attributes      map[string]string `yaml:"attributes"`
}

type MarktplaatsConfig struct {
	ZipCode       string        `yaml:"zip_code"`
	Distance      int           `yaml:"distance"`
	CheckInterval time.Duration `yaml:"check_interval"`
	RequestDelay  time.Duration `yaml:"request_delay"`
}

type DiscordConfig struct {
	WebhookURL        string   `yaml:"webhook_url"`
	AssistantEnabled  bool     `yaml:"assistant_enabled"`
	BotToken          string   `yaml:"bot_token"`
	CommandPrefix     string   `yaml:"command_prefix"`
	MessageContent    bool     `yaml:"message_content_enabled"`
	GuildIDs          []string `yaml:"guild_ids"`
	AllowedChannelIDs []string `yaml:"allowed_channel_ids"`
	AllowedUserIDs    []string `yaml:"allowed_user_ids"`
}

type MessengerConfig struct {
	Enabled            bool   `yaml:"enabled"`
	Headless           bool   `yaml:"headless"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	MaxMessagesPerHour int    `yaml:"max_messages_per_hour"`
}

type ScoringConfig struct {
	MinScore         float64 `yaml:"min_score"`
	MarketSampleSize int     `yaml:"market_sample_size"`
}

func (c ScoringConfig) GetMinScore() float64 {
	return c.MinScore
}

func (c ScoringConfig) GetMarketSampleSize() int {
	return c.MarketSampleSize
}

type AIConfig struct {
	Enabled                bool    `yaml:"enabled"`
	BaseURL                string  `yaml:"base_url"`
	APIKey                 string  `yaml:"api_key"`
	Model                  string  `yaml:"model"`
	Temperature            float64 `yaml:"temperature"`
	MaxComparables         int     `yaml:"max_comparables"`
	MinConfidence          float64 `yaml:"min_confidence"`
	SearchAdvice           bool    `yaml:"search_advice"`
	SkipLLMConfidence      float64 `yaml:"skip_llm_confidence"`
	SkipLLMScoreLow        float64 `yaml:"skip_llm_score_low"`
	SkipLLMScoreHigh       float64 `yaml:"skip_llm_score_high"`
	MaxCallsPerUserPerHour int     `yaml:"max_calls_per_user_per_hour"`
	MaxCallsGlobalPerHour  int     `yaml:"max_calls_global_per_hour"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	setDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

func LoadForGeneration(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	setDefaults(cfg)
	return cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Marktplaats.CheckInterval == 0 {
		cfg.Marktplaats.CheckInterval = 5 * time.Minute
	}
	if cfg.Marktplaats.RequestDelay == 0 {
		cfg.Marktplaats.RequestDelay = 3 * time.Second
	}
	if cfg.Marktplaats.Distance == 0 {
		cfg.Marktplaats.Distance = 100000
	}
	if cfg.Scoring.MinScore == 0 {
		cfg.Scoring.MinScore = 7.0
	}
	if cfg.Scoring.MarketSampleSize == 0 {
		cfg.Scoring.MarketSampleSize = 20
	}
	if cfg.Messenger.MaxMessagesPerHour == 0 {
		cfg.Messenger.MaxMessagesPerHour = 10
	}
	if cfg.Discord.CommandPrefix == "" {
		cfg.Discord.CommandPrefix = "!"
	}
	if cfg.AI.BaseURL == "" {
		cfg.AI.BaseURL = "https://api.openai.com/v1"
	}
	cfg.AI = NormalizeAIConfig(cfg.AI)
	for i := range cfg.Searches {
		if cfg.Searches[i].MarketplaceID == "" {
			cfg.Searches[i].MarketplaceID = "marktplaats"
		}
		if cfg.Searches[i].OfferPercentage == 0 {
			cfg.Searches[i].OfferPercentage = 70
		}
	}
}

func NormalizeAIConfig(cfg AIConfig) AIConfig {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.2
	}
	if cfg.MaxComparables == 0 {
		cfg.MaxComparables = 5
	}
	if cfg.MinConfidence == 0 {
		cfg.MinConfidence = 0.55
	}
	if cfg.SkipLLMConfidence == 0 {
		cfg.SkipLLMConfidence = 0.75
	}
	if cfg.SkipLLMScoreLow == 0 {
		cfg.SkipLLMScoreLow = 3.0
	}
	if cfg.SkipLLMScoreHigh == 0 {
		cfg.SkipLLMScoreHigh = 9.0
	}
	if cfg.MaxCallsPerUserPerHour == 0 {
		cfg.MaxCallsPerUserPerHour = 200
	}
	if cfg.MaxCallsGlobalPerHour == 0 {
		cfg.MaxCallsGlobalPerHour = 2000
	}
	return cfg
}

func validate(cfg *Config) error {
	if len(cfg.Searches) == 0 && !cfg.Discord.AssistantEnabled {
		return fmt.Errorf("at least one search must be configured")
	}
	for i, s := range cfg.Searches {
		if s.Query == "" {
			return fmt.Errorf("search[%d]: query is required", i)
		}
		if s.MarketplaceID == "" {
			return fmt.Errorf("search[%d]: marketplace_id is required", i)
		}
		if s.OfferPercentage < 1 || s.OfferPercentage > 100 {
			return fmt.Errorf("search[%d]: offer_percentage must be between 1 and 100", i)
		}
	}
	if cfg.Messenger.Enabled {
		if cfg.Messenger.Username == "" || cfg.Messenger.Password == "" {
			return fmt.Errorf("messenger: username and password required when enabled")
		}
	}
	if cfg.Discord.AssistantEnabled && cfg.Discord.BotToken == "" {
		return fmt.Errorf("discord: bot_token is required when assistant_enabled is true")
	}
	if cfg.AI.Enabled {
		if cfg.AI.APIKey == "" {
			return fmt.Errorf("ai: api_key is required when enabled")
		}
		if cfg.AI.Model == "" {
			return fmt.Errorf("ai: model is required when enabled")
		}
		if cfg.AI.MaxComparables < 1 {
			return fmt.Errorf("ai: max_comparables must be >= 1")
		}
		if cfg.AI.MinConfidence < 0 || cfg.AI.MinConfidence > 1 {
			return fmt.Errorf("ai: min_confidence must be between 0 and 1")
		}
		if cfg.AI.SkipLLMConfidence < 0 || cfg.AI.SkipLLMConfidence > 1 {
			return fmt.Errorf("ai: skip_llm_confidence must be between 0 and 1")
		}
		if cfg.AI.SkipLLMScoreLow < 1 || cfg.AI.SkipLLMScoreLow > 10 {
			return fmt.Errorf("ai: skip_llm_score_low must be between 1 and 10")
		}
		if cfg.AI.SkipLLMScoreHigh < 1 || cfg.AI.SkipLLMScoreHigh > 10 {
			return fmt.Errorf("ai: skip_llm_score_high must be between 1 and 10")
		}
		if cfg.AI.SkipLLMScoreLow >= cfg.AI.SkipLLMScoreHigh {
			return fmt.Errorf("ai: skip_llm_score_low must be less than skip_llm_score_high")
		}
		if cfg.AI.MaxCallsPerUserPerHour < 0 {
			return fmt.Errorf("ai: max_calls_per_user_per_hour must be >= 0")
		}
		if cfg.AI.MaxCallsGlobalPerHour < 0 {
			return fmt.Errorf("ai: max_calls_global_per_hour must be >= 0")
		}
	}
	return nil
}

func (c SearchConfig) ToSpec() models.SearchSpec {
	return models.SearchSpec{
		Name:            c.Name,
		Query:           c.Query,
		MarketplaceID:   c.MarketplaceID,
		ProfileID:       c.ProfileID,
		CategoryID:      c.CategoryID,
		MaxPrice:        wholeEuroToCents(c.MaxPrice),
		MinPrice:        wholeEuroToCents(c.MinPrice),
		Condition:       normalizeConditions(c.Condition),
		OfferPercentage: c.OfferPercentage,
		AutoMessage:     c.AutoMessage,
		MessageTemplate: c.MessageTemplate,
		Attributes:      cloneMap(c.Attributes),
		Enabled:         true,
		CheckInterval:   5 * time.Minute,
	}
}

func SearchConfigsToSpecs(configs []SearchConfig) []models.SearchSpec {
	out := make([]models.SearchSpec, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, cfg.ToSpec())
	}
	return out
}

func normalizeConditions(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "nieuw", "new":
			out = append(out, "new")
		case "zo goed als nieuw", "like new", "as good as new":
			out = append(out, "like_new")
		case "gebruikt", "used", "good":
			out = append(out, "good")
		case "fair":
			out = append(out, "fair")
		default:
			if strings.TrimSpace(value) != "" {
				out = append(out, strings.ToLower(strings.TrimSpace(value)))
			}
		}
	}
	return out
}

func wholeEuroToCents(value int) int {
	if value <= 0 {
		return 0
	}
	return value * 100
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
