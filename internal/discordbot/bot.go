package discordbot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/TechXTT/marktbot/internal/assistant"
	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/format"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/bwmarrin/discordgo"
)

const maxDiscordMessageLength = 1900

type Bot struct {
	cfg       config.DiscordConfig
	assistant *assistant.Assistant
	session   *discordgo.Session
}

func New(cfg config.DiscordConfig, asst *assistant.Assistant) (*Bot, error) {
	if !cfg.AssistantEnabled {
		return nil, nil
	}

	session, err := discordgo.New("Bot " + cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages
	if cfg.MessageContent {
		session.Identify.Intents |= discordgo.IntentsMessageContent
	}

	bot := &Bot{
		cfg:       cfg,
		assistant: asst,
		session:   session,
	}
	session.AddHandler(bot.onMessageCreate)
	session.AddHandler(bot.onInteractionCreate)
	return bot, nil
}

func (b *Bot) Start(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("opening discord session: %w", err)
	}
	if err := b.registerCommands(); err != nil {
		_ = b.session.Close()
		return fmt.Errorf("registering slash commands: %w", err)
	}
	slog.Info("discord assistant bot started")

	go func() {
		<-ctx.Done()
		_ = b.Close()
	}()
	return nil
}

func (b *Bot) Close() error {
	if b == nil || b.session == nil {
		return nil
	}
	return b.session.Close()
}

func (b *Bot) registerCommands() error {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "brief",
			Description: "Create or update your shopping brief",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "request",
					Description: "What you want to buy",
					Required:    true,
				},
			},
		},
		{
			Name:        "profile",
			Description: "Show your active shopping brief",
		},
		{
			Name:        "matches",
			Description: "Show the best current matches",
		},
		{
			Name:        "why",
			Description: "Explain a listing",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "listing_id",
					Description: "Marktplaats item id",
					Required:    true,
				},
			},
		},
		{
			Name:        "shortlist",
			Description: "Show your shortlist",
		},
		{
			Name:        "shortlist-add",
			Description: "Save a listing to your shortlist",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "listing_id",
					Description: "Marktplaats item id",
					Required:    true,
				},
			},
		},
		{
			Name:        "compare",
			Description: "Compare shortlisted items",
		},
		{
			Name:        "draft",
			Description: "Draft a seller message without sending it",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "listing_id",
					Description: "Marktplaats item id",
					Required:    true,
				},
			},
		},
		{
			Name:        "help",
			Description: "Show shopping assistant help",
		},
		{
			Name:        "chat",
			Description: "Talk to the shopping assistant",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "What you want help with",
					Required:    true,
				},
			},
		},
	}

	targetGuilds := b.cfg.GuildIDs
	if len(targetGuilds) == 0 {
		targetGuilds = []string{""}
	}

	for _, guildID := range targetGuilds {
		if _, err := b.session.ApplicationCommandBulkOverwrite(b.session.State.User.ID, guildID, commands); err != nil {
			return fmt.Errorf("syncing slash commands for guild %q: %w", guildID, err)
		}
	}

	if len(b.cfg.GuildIDs) > 0 {
		if _, err := b.session.ApplicationCommandBulkOverwrite(b.session.State.User.ID, "", []*discordgo.ApplicationCommand{}); err != nil {
			slog.Warn("failed to clear global slash commands", "error", err)
		}
	}
	return nil
}

func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	userID := interactionUserID(i)
	channelID := interactionChannelID(i)
	if !b.isAllowed(userID, channelID) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You are not allowed to use this shopping assistant in this channel.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	reply := b.handleSlashCommand(i.ApplicationCommandData(), userID)
	if reply == "" {
		reply = "No response."
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		slog.Warn("failed to defer slash command response", "command", i.ApplicationCommandData().Name, "error", err)
		return
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: ptr(truncateDiscord(reply)),
	}); err != nil {
		slog.Warn("failed to edit slash command response", "command", i.ApplicationCommandData().Name, "error", err)
	}
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}
	if !b.isAllowed(m.Author.ID, m.ChannelID) {
		return
	}

	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}

	var reply string
	if strings.HasPrefix(content, b.cfg.CommandPrefix) {
		reply = b.handleTextCommand(m.Author.ID, strings.TrimPrefix(content, b.cfg.CommandPrefix))
	} else if b.cfg.MessageContent {
		conversationReply, err := b.assistant.Converse(context.Background(), m.Author.ID, content)
		if err != nil {
			reply = "I hit a problem while handling that request: " + err.Error()
		} else if conversationReply != nil {
			reply = conversationReply.Message
		}
	} else {
		return
	}

	if reply == "" {
		return
	}
	if _, err := s.ChannelMessageSend(m.ChannelID, truncateDiscord(reply)); err != nil {
		slog.Warn("failed to send discord assistant reply", "error", err)
	}
}

func (b *Bot) handleSlashCommand(data discordgo.ApplicationCommandInteractionData, userID string) string {
	ctx := context.Background()

	switch data.Name {
	case "help":
		return helpText(b.cfg.CommandPrefix)
	case "chat":
		message := optionString(data.Options, "message")
		reply, err := b.assistant.Converse(ctx, userID, message)
		if err != nil {
			return "I hit a problem while handling that request: " + err.Error()
		}
		return reply.Message
	case "brief":
		request := optionString(data.Options, "request")
		if request == "" {
			return "Usage: /brief request:<what you want to buy>"
		}
		reply, err := b.assistant.Converse(ctx, userID, request)
		if err != nil {
			return "I couldn't start that shopping brief: " + err.Error()
		}
		return reply.Message
	case "profile":
		return b.handleProfile(userID)
	case "matches":
		return b.handleMatches(ctx, userID)
	case "why":
		listingID := optionString(data.Options, "listing_id")
		if listingID == "" {
			return "Usage: /why listing_id:<listing_id>"
		}
		detail, err := b.assistant.ExplainListing(ctx, userID, listingID)
		if err != nil {
			return "I couldn't explain that listing: " + err.Error()
		}
		return detail
	case "shortlist":
		return b.handleShortlistList(userID)
	case "shortlist-add":
		listingID := optionString(data.Options, "listing_id")
		if listingID == "" {
			return "Usage: /shortlist-add listing_id:<listing_id>"
		}
		entry, err := b.assistant.SaveToShortlist(ctx, userID, listingID)
		if err != nil {
			return "I couldn't add that listing to the shortlist: " + err.Error()
		}
		return fmt.Sprintf("Saved to shortlist: %s [%s]", entry.Title, entry.RecommendationLabel)
	case "compare":
		comparison, err := b.assistant.CompareShortlist(ctx, userID)
		if err != nil {
			return "I couldn't compare your shortlist: " + err.Error()
		}
		return comparison
	case "draft":
		listingID := optionString(data.Options, "listing_id")
		if listingID == "" {
			return "Usage: /draft listing_id:<listing_id>"
		}
		draft, err := b.assistant.DraftSellerMessage(ctx, userID, listingID)
		if err != nil {
			return "I couldn't draft a seller message: " + err.Error()
		}
		return "Draft only, not sent:\n" + draft.Content
	default:
		return helpText(b.cfg.CommandPrefix)
	}
}

func (b *Bot) handleTextCommand(userID, content string) string {
	fields := strings.Fields(content)
	if len(fields) == 0 {
		return helpText(b.cfg.CommandPrefix)
	}

	command := strings.ToLower(fields[0])
	args := strings.TrimSpace(strings.TrimPrefix(content, fields[0]))
	ctx := context.Background()

	switch command {
	case "help":
		return helpText(b.cfg.CommandPrefix)
	case "chat":
		reply, err := b.assistant.Converse(ctx, userID, args)
		if err != nil {
			return "I hit a problem while handling that request: " + err.Error()
		}
		return reply.Message
	case "brief":
		if args == "" {
			return "Usage: " + b.cfg.CommandPrefix + "brief <what you want to buy>"
		}
		reply, err := b.assistant.Converse(ctx, userID, args)
		if err != nil {
			return "I couldn't save that shopping brief: " + err.Error()
		}
		return reply.Message
	case "profile":
		return b.handleProfile(userID)
	case "matches":
		return b.handleMatches(ctx, userID)
	case "why":
		if args == "" {
			return "Usage: " + b.cfg.CommandPrefix + "why <listing_id>"
		}
		detail, err := b.assistant.ExplainListing(ctx, userID, strings.TrimSpace(args))
		if err != nil {
			return "I couldn't explain that listing: " + err.Error()
		}
		return detail
	case "shortlist":
		return b.handleShortlistText(ctx, userID, args)
	case "compare":
		comparison, err := b.assistant.CompareShortlist(ctx, userID)
		if err != nil {
			return "I couldn't compare your shortlist: " + err.Error()
		}
		return comparison
	case "draft":
		if args == "" {
			return "Usage: " + b.cfg.CommandPrefix + "draft <listing_id>"
		}
		draft, err := b.assistant.DraftSellerMessage(ctx, userID, strings.TrimSpace(args))
		if err != nil {
			return "I couldn't draft a seller message: " + err.Error()
		}
		return "Draft only, not sent:\n" + draft.Content
	default:
		return helpText(b.cfg.CommandPrefix)
	}
}

func (b *Bot) handleProfile(userID string) string {
	profile, err := b.assistant.GetActiveProfile(userID)
	if err != nil {
		return "I couldn't load your active brief: " + err.Error()
	}
	if profile == nil {
		return "No active shopping brief yet. Start with /brief or " + b.cfg.CommandPrefix + "brief."
	}
	return fmt.Sprintf(
		"Active brief: %s\nQuery: %s\nSearches: %s\nBudget max/stretch: %d/%d",
		profile.Name,
		profile.TargetQuery,
		strings.Join(profile.SearchQueries, ", "),
		profile.BudgetMax,
		profile.BudgetStretch,
	)
}

func (b *Bot) handleMatches(ctx context.Context, userID string) string {
	recs, profile, err := b.assistant.FindMatches(ctx, userID, 5)
	if err != nil {
		return "I couldn't fetch matches: " + err.Error()
	}
	if len(recs) == 0 {
		return "I couldn't find any suitable matches for your active brief right now."
	}
	return renderMatches(profile.Name, recs)
}

func (b *Bot) handleShortlistList(userID string) string {
	entries, err := b.assistant.ListShortlist(userID)
	if err != nil {
		return "I couldn't load your shortlist: " + err.Error()
	}
	if len(entries) == 0 {
		return "Your shortlist is empty. Use /shortlist-add or " + b.cfg.CommandPrefix + "shortlist add <listing_id>."
	}
	return renderShortlist(entries)
}

func (b *Bot) handleShortlistText(ctx context.Context, userID, args string) string {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return b.handleShortlistList(userID)
	}

	switch strings.ToLower(fields[0]) {
	case "add":
		if len(fields) < 2 {
			return "Usage: " + b.cfg.CommandPrefix + "shortlist add <listing_id>"
		}
		entry, err := b.assistant.SaveToShortlist(ctx, userID, fields[1])
		if err != nil {
			return "I couldn't add that listing to the shortlist: " + err.Error()
		}
		return fmt.Sprintf("Saved to shortlist: %s [%s]", entry.Title, entry.RecommendationLabel)
	case "list":
		return b.handleShortlistList(userID)
	default:
		return "Usage: " + b.cfg.CommandPrefix + "shortlist add <listing_id> or " + b.cfg.CommandPrefix + "shortlist list"
	}
}

func (b *Bot) isAllowed(userID, channelID string) bool {
	if len(b.cfg.AllowedUserIDs) > 0 && !contains(b.cfg.AllowedUserIDs, userID) {
		return false
	}
	if len(b.cfg.AllowedChannelIDs) > 0 && !contains(b.cfg.AllowedChannelIDs, channelID) {
		return false
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func optionString(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, option := range options {
		if option.Name == name {
			if value, ok := option.Value.(string); ok {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func interactionChannelID(i *discordgo.InteractionCreate) string {
	return i.ChannelID
}

func helpText(prefix string) string {
	return strings.Join([]string{
		"Shopping assistant commands:",
		"/brief request:<what you want to buy>",
		"/profile",
		"/matches",
		"/why listing_id:<listing_id>",
		"/shortlist",
		"/shortlist-add listing_id:<listing_id>",
		"/compare",
		"/draft listing_id:<listing_id>",
		"/chat message:<what you need>",
		"",
		"Text fallback commands still supported when available:",
		prefix + "brief <request>",
		prefix + "chat <message>",
		prefix + "matches",
		prefix + "shortlist add <listing_id>",
	}, "\n")
}

func formatProfileSaved(profile *models.ShoppingProfile) string {
	return fmt.Sprintf(
		"Saved shopping brief: %s\nQuery: %s\nBudget max: %d\nStretch: %d",
		profile.Name,
		profile.TargetQuery,
		profile.BudgetMax,
		profile.BudgetStretch,
	)
}

func renderMatches(profileName string, recs []models.Recommendation) string {
	lines := []string{fmt.Sprintf("Top matches for %s:", profileName), ""}
	for _, rec := range recs {
		lines = append(lines, fmt.Sprintf("• %s", rec.Listing.Title))
		lines = append(lines, fmt.Sprintf("  %s", friendlyLabel(rec.Label)))
		lines = append(lines, fmt.Sprintf("  Ask: %s", formatEuro(rec.Listing.Price)))
		lines = append(lines, fmt.Sprintf("  Fair value: %s", formatEuro(rec.Scored.FairPrice)))
		lines = append(lines, fmt.Sprintf("  Item ID: `%s`", rec.Listing.ItemID))
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func renderShortlist(entries []models.ShortlistEntry) string {
	lines := []string{"Shortlist:", ""}
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf("• %s", entry.Title))
		lines = append(lines, fmt.Sprintf("  %s", friendlyLabel(entry.RecommendationLabel)))
		lines = append(lines, fmt.Sprintf("  Ask: %s", formatEuro(entry.AskPrice)))
		lines = append(lines, fmt.Sprintf("  Fair value: %s", formatEuro(entry.FairPrice)))
		lines = append(lines, fmt.Sprintf("  Item ID: `%s`", entry.ItemID))
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func truncateDiscord(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxDiscordMessageLength {
		return value
	}
	return strings.TrimSpace(value[:maxDiscordMessageLength-3]) + "..."
}

func formatEuro(cents int) string {
	return format.Euro(cents)
}

func ptr[T any](value T) *T {
	return &value
}

func friendlyLabel(label models.RecommendationLabel) string {
	switch label {
	case models.RecommendationBuyNow:
		return "Buy now"
	case models.RecommendationWatch:
		return "Worth watching"
	case models.RecommendationAskQuestions:
		return "Ask questions first"
	default:
		return "Skip"
	}
}
