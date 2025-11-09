package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"feedback_bot/internal/scheduler"
	"feedback_bot/internal/service"
	"feedback_bot/internal/storage"
	"feedback_bot/internal/wbapi"
	"feedback_bot/pkg/metrics"
)

// UserState represents the current state of user in configuration flow
type UserState int

const (
	StateIdle UserState = iota
	StateWaitingToken
	StateWaitingTemplateGood
	StateWaitingTemplateBad
	StateReady
)

// Callback button data prefixes
const (
	CallbackMainMenu          = "main_menu"
	CallbackAddToken          = "add_token"
	CallbackAddTemplateGood   = "add_template_good"
	CallbackAddTemplateBad    = "add_template_bad"
	CallbackViewInfo          = "view_info"
	CallbackDeleteAll         = "delete_all"
	CallbackCancel            = "cancel"
	CallbackConfirmDelete     = "confirm_delete"
	CallbackRunNow            = "run_now"
	CallbackCheckSubscription = "check_subscription"
)

// Constants for DoS protection
const (
	// MaxRequestsPerMinute limits requests per user per minute
	MaxRequestsPerMinute = 30
	// MaxBurstSize allows burst of requests
	MaxBurstSize = 10
	// MaxTemplateLength limits template size in characters
	MaxTemplateLength = 10000
	// MinTokenLength minimum token length
	MinTokenLength = 20
	// MaxTokenLength maximum token length (JWT tokens can be 500-1000 chars)
	MaxTokenLength = 2000
)

// Bot handles Telegram commands and configuration flow.
type Bot struct {
	api         *tgbotapi.BotAPI
	log         *zap.SugaredLogger
	ctx         context.Context
	configStore storage.ConfigStore
	userStore   storage.Store

	// User states for configuration flow
	userStates map[int64]UserState
	userConfig map[int64]*storage.UserConfig // Temporary storage during setup
	mu         sync.RWMutex

	// Service creation dependencies
	wbBaseURL    string
	pollInterval string

	// Per-user services and schedulers for multi-user support
	services   map[int64]*service.Service
	schedulers map[int64]*scheduler.Scheduler
	svcMu      sync.RWMutex // mutex for services and schedulers maps

	// DoS protection: rate limiting per user
	userRateLimiters map[int64]*rate.Limiter
	rateLimitMu      sync.RWMutex

	// DoS protection: semaphore for concurrent goroutines
	goroutineSemaphore chan struct{}

	// Channel subscription check
	requiredChannel   string // Telegram channel username (e.g., "@channel" or "novikovpromarket")
	requiredChannelID int64  // Telegram channel ID (numeric). If set, used directly for GetChatMember
	adminUserID       int64  // Admin user ID for /admin command access

	// Subscription cache: map[userID] = {isSubscribed: bool, expiresAt: time.Time}
	subscriptionCache map[int64]struct {
		isSubscribed bool
		expiresAt    time.Time
	}
	subscriptionCacheMu sync.RWMutex
}

// New creates a new Telegram bot instance.
// Telegram token is now required.
func New(token string, configStore storage.ConfigStore, userStore storage.Store, logger *zap.SugaredLogger, ctx context.Context, requiredChannel string, requiredChannelID int64, adminUserID int64) (*Bot, error) {
	if token == "" {
		return nil, fmt.Errorf("telegram token is required")
	}

	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	if logger == nil {
		logger = zap.NewNop().Sugar()
	}

	// Normalize channel username (remove @ if present, add @ if missing)
	channel := strings.TrimSpace(requiredChannel)
	if channel != "" && !strings.HasPrefix(channel, "@") {
		channel = "@" + channel
	}

	bot := &Bot{
		api:                api,
		log:                logger,
		ctx:                ctx,
		configStore:        configStore,
		userStore:          userStore,
		userStates:         make(map[int64]UserState),
		userConfig:         make(map[int64]*storage.UserConfig),
		wbBaseURL:          "https://feedbacks-api.wildberries.ru",
		pollInterval:       "10m",
		services:           make(map[int64]*service.Service),
		schedulers:         make(map[int64]*scheduler.Scheduler),
		userRateLimiters:   make(map[int64]*rate.Limiter),
		goroutineSemaphore: make(chan struct{}, 100), // максимум 100 одновременных горутин
		requiredChannel:    channel,
		requiredChannelID:  requiredChannelID,
		adminUserID:        adminUserID,
		subscriptionCache: make(map[int64]struct {
			isSubscribed bool
			expiresAt    time.Time
		}),
	}

	// Log subscription check configuration
	if requiredChannelID != 0 || channel != "" {
		if requiredChannelID != 0 {
			logger.Infow("✅ SUBSCRIPTION CHECK ENABLED",
				"channel_id", requiredChannelID,
				"channel_username", channel,
				"important", "Bot must be administrator in the channel to check subscriptions")
		} else {
			logger.Infow("✅ SUBSCRIPTION CHECK ENABLED",
				"channel_username", channel,
				"tip", "Consider using REQUIRED_CHANNEL_ID for better performance",
				"important", "Bot must be administrator in the channel to check subscriptions")
		}
	} else {
		logger.Warnw("⚠️ SUBSCRIPTION CHECK DISABLED - no channel configured",
			"tip", "Set REQUIRED_CHANNEL_ID or REQUIRED_CHANNEL to enable subscription check",
			"warning", "All users will have access without subscription check")
	}

	bot.log.Infow("telegram bot authorized", "username", api.Self.UserName)
	return bot, nil
}

// getUserRateLimiter returns or creates a rate limiter for the user
func (b *Bot) getUserRateLimiter(userID int64) *rate.Limiter {
	b.rateLimitMu.Lock()
	defer b.rateLimitMu.Unlock()

	limiter, exists := b.userRateLimiters[userID]
	if !exists {
		// Allow MaxRequestsPerMinute requests per minute with burst of MaxBurstSize
		limiter = rate.NewLimiter(rate.Limit(MaxRequestsPerMinute)/60, MaxBurstSize)
		b.userRateLimiters[userID] = limiter
	}
	return limiter
}

// checkRateLimit checks if user exceeded rate limit
func (b *Bot) checkRateLimit(userID int64) bool {
	limiter := b.getUserRateLimiter(userID)
	return limiter.Allow()
}

// isValidTokenFormat validates token format (alphanumeric and common token characters)
func isValidTokenFormat(token string) bool {
	if len(token) == 0 {
		return false
	}

	// Check if token contains only valid characters
	// Allow: letters, digits, dots, dashes, underscores
	for _, r := range token {
		if !((r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Run starts the bot's update loop. It blocks until context is cancelled.
func (b *Bot) Run(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	b.log.Info("telegram bot started, waiting for commands")

	// Start cleanup goroutine for inactive users (runs every hour)
	go b.cleanupInactiveUsers(ctx)

	for {
		select {
		case <-ctx.Done():
			b.log.Info("telegram bot: context cancelled, stopping")
			b.api.StopReceivingUpdates()
			return
		case update := <-updates:
			// Use semaphore to limit concurrent goroutines
			select {
			case b.goroutineSemaphore <- struct{}{}:
				// Got slot, process update
				if update.CallbackQuery != nil {
					go func() {
						defer func() {
							<-b.goroutineSemaphore
							// Panic recovery
							if r := recover(); r != nil {
								b.log.Errorw("panic recovered in handleCallbackQuery",
									"chat_id", update.CallbackQuery.Message.Chat.ID,
									"panic", r,
									"update_id", update.UpdateID)
							}
						}()
						b.handleCallbackQuery(ctx, update.CallbackQuery)
					}()
				} else if update.Message != nil {
					go func() {
						defer func() {
							<-b.goroutineSemaphore
							// Panic recovery
							if r := recover(); r != nil {
								b.log.Errorw("panic recovered in handleMessage",
									"chat_id", update.Message.Chat.ID,
									"panic", r,
									"update_id", update.UpdateID)
							}
						}()
						b.handleMessage(ctx, update.Message)
					}()
				}
			case <-ctx.Done():
				return
			default:
				// Semaphore full - log warning and skip
				b.log.Warnw("goroutine semaphore full, skipping update", "update_id", update.UpdateID)
			}
		}
	}
}

// SendMessage sends a message to the specified chat ID.
func (b *Bot) SendMessage(chatID int64, text string) error {
	// Validate UTF-8 encoding before sending
	if !utf8.ValidString(text) {
		b.log.Warnw("invalid UTF-8 string detected, cleaning", "chat_id", chatID)
		// Clean invalid UTF-8 sequences
		text = strings.ToValidUTF8(text, "")
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	_, err := b.api.Send(msg)
	if err != nil {
		b.log.Warnw("failed to send telegram message", "chat_id", chatID, "err", err)
		metrics.IncrementAPIError("telegram", "send_message")
		return err
	}
	return nil
}

// SendMessageWithKeyboard sends a message with inline keyboard
func (b *Bot) SendMessageWithKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) error {
	// Validate UTF-8 encoding before sending
	if !utf8.ValidString(text) {
		b.log.Warnw("invalid UTF-8 string detected, cleaning", "chat_id", chatID)
		// Clean invalid UTF-8 sequences
		text = strings.ToValidUTF8(text, "")
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = keyboard
	_, err := b.api.Send(msg)
	if err != nil {
		b.log.Warnw("failed to send telegram message with keyboard", "chat_id", chatID, "err", err)
		return err
	}
	return nil
}

// CreateMainMenu creates the main menu keyboard
func (b *Bot) CreateMainMenu() tgbotapi.InlineKeyboardMarkup {
	// Simple menu without user-specific info
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Информация", CallbackViewInfo),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔑 Добавить токен WB", CallbackAddToken),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Добавить ответ (позитив)", CallbackAddTemplateGood),
			tgbotapi.NewInlineKeyboardButtonData("❌ Добавить ответ (негатив)", CallbackAddTemplateBad),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 Запустить программу", CallbackRunNow),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 СТЕРЕТЬ ВСЮ ИНФОРМАЦИЮ", CallbackDeleteAll),
		),
	)
}

// CreateMainMenuForUser creates the main menu keyboard with user-specific information
// Shows buttons progressively: marketplace -> tokens -> templates -> run
func (b *Bot) CreateMainMenuForUser(chatID int64) tgbotapi.InlineKeyboardMarkup {
	// Use context with timeout for DB query
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, _ := b.configStore.GetUserConfig(ctx, chatID)

	var keyboard [][]tgbotapi.InlineKeyboardButton

	// Always show information button
	keyboard = append(keyboard, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("📋 Информация", CallbackViewInfo),
	})

	// Token button
	keyboard = append(keyboard, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("🔑 Добавить токен WB", CallbackAddToken),
	})

	// Template buttons (only if token is set)
	hasToken := cfg != nil && cfg.WBToken != "" && cfg.WBToken != "not_set"
	if hasToken {
		keyboard = append(keyboard, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("✅ Добавить ответ (позитив)", CallbackAddTemplateGood),
			tgbotapi.NewInlineKeyboardButtonData("❌ Добавить ответ (негатив)", CallbackAddTemplateBad),
		})

		// Run button (only if everything is configured)
		hasTemplates := cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" &&
			cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!"

		if hasTemplates {
			keyboard = append(keyboard, []tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("🚀 Запустить программу", CallbackRunNow),
			})
		}
	}

	// Always show delete button (if config exists)
	if cfg != nil {
		keyboard = append(keyboard, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("🗑 СТЕРЕТЬ ВСЮ ИНФОРМАЦИЮ", CallbackDeleteAll),
		})
	}

	return tgbotapi.NewInlineKeyboardMarkup(keyboard...)
}

// CreateCancelKeyboard creates a cancel button
func (b *Bot) CreateCancelKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отменить", CallbackCancel),
		),
	)
}

// CreateConfirmDeleteKeyboard creates confirmation buttons for delete
func (b *Bot) CreateConfirmDeleteKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Да, удалить", CallbackConfirmDelete),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отменить", CallbackCancel),
		),
	)
}

func (b *Bot) handleCallbackQuery(ctx context.Context, query *tgbotapi.CallbackQuery) {
	chatID := query.Message.Chat.ID
	data := query.Data

	// Answer callback query to remove loading state
	b.api.Request(tgbotapi.NewCallback(query.ID, ""))

	// Check rate limit
	if !b.checkRateLimit(chatID) {
		b.log.Warnw("rate limit exceeded", "chat_id", chatID, "callback", data)
		metrics.IncrementRateLimitHit(chatID)
		b.SendMessage(chatID, "⚠️ *Превышен лимит запросов*\n\nПожалуйста, подождите немного перед следующим запросом.")
		return
	}

	b.log.Debugw("received callback query", "chat_id", chatID, "data", data)

	switch data {
	case CallbackMainMenu:
		// Check subscription before showing main menu
		if !b.checkChannelSubscription(chatID) {
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.showMainMenu(chatID)
	case CallbackViewInfo:
		if !b.checkChannelSubscription(chatID) {
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.handleViewInfo(chatID, ctx)
	case CallbackAddToken:
		if !b.checkChannelSubscription(chatID) {
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.handleAddTokenButton(chatID)
	case CallbackAddTemplateGood:
		if !b.checkChannelSubscription(chatID) {
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.handleAddTemplateGoodButton(chatID)
	case CallbackAddTemplateBad:
		if !b.checkChannelSubscription(chatID) {
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.handleAddTemplateBadButton(chatID)
	case CallbackDeleteAll:
		if !b.checkChannelSubscription(chatID) {
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.handleDeleteAllButton(chatID)
	case CallbackConfirmDelete:
		b.log.Infow("CallbackConfirmDelete received", "chat_id", chatID)
		if !b.checkChannelSubscription(chatID) {
			b.log.Warnw("subscription check failed for delete", "chat_id", chatID)
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.log.Infow("subscription check passed, calling handleConfirmDelete", "chat_id", chatID)
		b.handleConfirmDelete(chatID, ctx)
	case CallbackCancel:
		// Check subscription before canceling
		if !b.checkChannelSubscription(chatID) {
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.handleCancel(chatID)
	case CallbackRunNow:
		if !b.checkChannelSubscription(chatID) {
			b.sendChannelSubscriptionMessage(chatID)
			return
		}
		b.handleRunNowButton(chatID, ctx)
	case CallbackCheckSubscription:
		b.handleCheckSubscription(chatID)
	default:
		b.SendMessage(chatID, "❓ Неизвестная команда")
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	if msg == nil || msg.Text == "" {
		return
	}

	command := strings.ToLower(strings.TrimSpace(msg.Text))
	chatID := msg.Chat.ID

	// Check rate limit
	if !b.checkRateLimit(chatID) {
		b.log.Warnw("rate limit exceeded", "chat_id", chatID, "command", command)
		metrics.IncrementRateLimitHit(chatID)
		b.SendMessage(chatID, "⚠️ *Превышен лимит запросов*\n\nПожалуйста, подождите немного перед следующим запросом.")
		return
	}

	b.log.Debugw("received telegram message", "chat_id", chatID, "command", command)

	// Handle commands
	if strings.HasPrefix(command, "/") {
		switch {
		case command == "/start" || command == "/help":
			b.showMainMenu(chatID)
			return
		case command == "/status":
			// Check subscription before allowing access
			if !b.checkChannelSubscription(chatID) {
				b.sendChannelSubscriptionMessage(chatID)
				return
			}
			b.handleViewInfo(chatID, ctx)
			return
		case command == "/run" || command == "/run_now":
			// Check subscription before allowing access
			if !b.checkChannelSubscription(chatID) {
				b.sendChannelSubscriptionMessage(chatID)
				return
			}
			b.handleRunNow(chatID, ctx)
			return
		case command == "/admin":
			// Admin command - check if user is admin
			b.handleAdminCommand(chatID, ctx)
			return
		}
	}

	// Check subscription for all other messages
	if !b.checkChannelSubscription(chatID) {
		b.sendChannelSubscriptionMessage(chatID)
		return
	}

	// Handle configuration flow based on state
	state := b.getUserState(chatID)
	switch state {
	case StateIdle:
		// Show main menu for any text input
		b.showMainMenu(chatID)
	case StateWaitingToken:
		b.handleTokenInput(chatID, msg.Text, ctx)
	case StateWaitingTemplateGood:
		b.handleTemplateGoodInput(chatID, msg.Text, ctx)
	case StateWaitingTemplateBad:
		b.handleTemplateBadInput(chatID, msg.Text, ctx)
	case StateReady:
		b.showMainMenu(chatID)
	}
}

func (b *Bot) showMainMenu(chatID int64) {
	// Check channel subscription first (cached, so it's fast)
	if !b.checkChannelSubscription(chatID) {
		b.sendChannelSubscriptionMessage(chatID)
		return
	}

	// Use context with timeout for DB query
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, _ := b.configStore.GetUserConfig(dbCtx, chatID)

	var msg string

	if cfg == nil {
		// No config yet
		msg = `🤖 *Добро пожаловать!
		
Это БЕСПЛАТНЫЙ Автоответчик на отзывы Wildberries.*

Для начала работы тебе следует выполнить ряд действий:

1) Добавить токен Wildberries.

2) Добавить шаблоны ответов.

3) 🚀 Запустите программу.

Важно все делать по инструкции
ИНАЧЕ БОТ НЕ БУДЕТ РАБОТАТЬ. 

Если возникли проблемы / вопросы:
Пиши =>  @RyslanNovikov`

	} else {
		// Check configuration status
		hasToken := cfg.WBToken != "" && cfg.WBToken != "not_set"
		hasTemplates := cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" &&
			cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!"

		msg = `🤖 *Автоответчик на отзывы Wildberries*

Текущий статус настройки:`

		if !hasToken {
			msg += "\n\n⚠️ *Шаг 1:* Добавьте токен WB ⏳"
			msg += "\n⚠️ *Шаг 2:* Добавьте шаблоны ответов ⏳"
		} else if !hasTemplates {
			msg += "\n\n✅ *Шаг 1:* Токен добавлен ✅"
			msg += "\n⚠️ *Шаг 2:* Добавьте шаблоны ответов ⏳"
		} else {
			msg += "\n\n✅ *Шаг 1:* Токен добавлен ✅"
			msg += "\n✅ *Шаг 2:* Шаблоны добавлены ✅"
			msg += "\n\n🎉 *Бот готов к работе!*"
		}
	}

	b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenuForUser(chatID))
}

// checkChannelSubscription checks if user is subscribed to the required channel
// Uses channel ID directly if available (faster and more reliable), otherwise uses username
// Results are cached for 5 minutes to reduce API calls and log noise
func (b *Bot) checkChannelSubscription(chatID int64) bool {
	// Check cache first
	b.subscriptionCacheMu.RLock()
	cached, exists := b.subscriptionCache[chatID]
	if exists && time.Now().Before(cached.expiresAt) {
		b.subscriptionCacheMu.RUnlock()
		b.log.Debugw("subscription check from cache",
			"chat_id", chatID,
			"is_subscribed", cached.isSubscribed,
			"cache_expires_at", cached.expiresAt)
		return cached.isSubscribed
	}
	b.subscriptionCacheMu.RUnlock()

	b.log.Infow("performing fresh subscription check",
		"chat_id", chatID,
		"channel_id", b.requiredChannelID,
		"channel_username", b.requiredChannel)

	// If no channel requirement set, allow access silently (for backwards compatibility)
	// Don't log warning on every check - only log once at startup
	if b.requiredChannelID == 0 && b.requiredChannel == "" {
		b.log.Debugw("subscription check skipped - no channel configured",
			"chat_id", chatID,
			"tip", "Set REQUIRED_CHANNEL_ID or REQUIRED_CHANNEL to enable subscription check")
		return true // Allow access if no channel requirement
	}

	var channelChatID int64
	var channelIdentifier string

	// Use channel ID directly if available (preferred method, like in Python code)
	if b.requiredChannelID != 0 {
		channelChatID = b.requiredChannelID
		channelIdentifier = fmt.Sprintf("ID:%d", b.requiredChannelID)
		b.log.Infow("checking subscription using channel ID",
			"chat_id", chatID,
			"channel_id", channelChatID,
			"channel_username", b.requiredChannel)
	} else {
		// Fallback to username method
		channelUsername := strings.TrimPrefix(b.requiredChannel, "@")
		channelIdentifier = b.requiredChannel

		b.log.Infow("getting channel ID from username",
			"chat_id", chatID,
			"channel", b.requiredChannel,
			"username", channelUsername)

		// Get chat info by username to obtain chat ID
		chatConfig := tgbotapi.ChatInfoConfig{
			ChatConfig: tgbotapi.ChatConfig{
				SuperGroupUsername: channelUsername,
			},
		}

		chat, err := b.api.GetChat(chatConfig)
		if err != nil {
			b.log.Errorw("FAILED: Cannot get channel info - bot may not have access",
				"channel", b.requiredChannel,
				"username", channelUsername,
				"chat_id", chatID,
				"error", err.Error(),
				"tip", "Try using REQUIRED_CHANNEL_ID instead, or ensure bot is admin in the channel")
			return false
		}

		channelChatID = chat.ID
		b.log.Infow("channel ID retrieved from username",
			"chat_id", chatID,
			"channel_id", channelChatID,
			"channel_title", chat.Title)
	}

	// Check if user is member of the channel (like in Python: bot.get_chat_member(chan_id, user_id))
	memberConfig := tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: channelChatID,
			UserID: chatID,
		},
	}

	member, err := b.api.GetChatMember(memberConfig)
	if err != nil {
		b.log.Errorw("FAILED: Cannot check subscription - bot must be administrator in the channel!",
			"chat_id", chatID,
			"channel", channelIdentifier,
			"channel_id", channelChatID,
			"error", err.Error(),
			"solution", "Bot must be added as administrator to the channel with permission to view members")
		return false
	}

	// Check if user is member, administrator, or creator (like in Python code)
	status := member.Status
	isSubscribed := status == "member" || status == "administrator" || status == "creator"

	// Log at info level for better diagnostics
	b.log.Infow("subscription check result",
		"chat_id", chatID,
		"channel", channelIdentifier,
		"channel_id", channelChatID,
		"user_status", status,
		"is_subscribed", isSubscribed)

	// Cache result for 5 minutes
	b.subscriptionCacheMu.Lock()
	b.subscriptionCache[chatID] = struct {
		isSubscribed bool
		expiresAt    time.Time
	}{
		isSubscribed: isSubscribed,
		expiresAt:    time.Now().Add(5 * time.Minute),
	}
	b.subscriptionCacheMu.Unlock()

	if !isSubscribed {
		b.log.Warnw("user is NOT subscribed to the channel",
			"chat_id", chatID,
			"channel", channelIdentifier,
			"channel_id", channelChatID,
			"user_status", status,
			"allowed_statuses", "member, administrator, creator")
	}

	return isSubscribed
}

// sendChannelSubscriptionMessage sends a message asking user to subscribe
func (b *Bot) sendChannelSubscriptionMessage(chatID int64) {
	b.log.Infow("sending channel subscription message", "chat_id", chatID)

	// Use username for URL (even if we use ID for checking)
	var channelUsername string
	var channelDisplay string
	if b.requiredChannel != "" {
		channelUsername = strings.TrimPrefix(b.requiredChannel, "@")
		channelDisplay = "@" + channelUsername
	} else if b.requiredChannelID != 0 {
		// If only ID is set, try to construct URL
		channelUsername = "novikovpromarket" // fallback - should be set via REQUIRED_CHANNEL
		channelDisplay = fmt.Sprintf("канал (ID: %d)", b.requiredChannelID)
		b.log.Warnw("channel username not set, using fallback",
			"channel_id", b.requiredChannelID,
			"tip", "Set REQUIRED_CHANNEL environment variable for better user experience")
	} else {
		// This shouldn't happen, but handle it gracefully
		channelUsername = "novikovpromarket"
		channelDisplay = "канал"
		b.log.Errorw("neither channel ID nor username is set",
			"chat_id", chatID,
			"warning", "Subscription check should not be called without channel configuration")
	}
	channelURL := "https://t.me/" + channelUsername

	b.log.Infow("subscription message details",
		"chat_id", chatID,
		"channel_username", channelUsername,
		"channel_id", b.requiredChannelID,
		"channel_url", channelURL)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("📢 Подписаться на канал", channelURL),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Я подписался, проверить", "check_subscription"),
		),
	)

	msg := fmt.Sprintf(`🔒 *Доступ ограничен*

Для использования бота необходимо подписаться на наш канал:

📢 *%s*

После подписки нажмите кнопку "✅ Я подписался, проверить" для проверки.`,
		channelDisplay)

	message := tgbotapi.NewMessage(chatID, msg)
	message.ParseMode = tgbotapi.ModeMarkdown
	message.ReplyMarkup = keyboard
	if _, err := b.api.Send(message); err != nil {
		b.log.Errorw("failed to send subscription message",
			"chat_id", chatID,
			"error", err.Error())
	}
}

func (b *Bot) handleViewInfo(chatID int64, ctx context.Context) {
	// Use context with timeout for DB query to avoid hanging
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := b.configStore.GetUserConfig(dbCtx, chatID)
	if err != nil {
		b.log.Warnw("failed to get user config for info", "chat_id", chatID, "err", err)
		metrics.IncrementDatabaseError("get_config")
		msg := `❌ *Ошибка при получении информации*

Попробуйте позже или обратитесь к администратору.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
		return
	}

	if cfg == nil {
		msg := `❌ *Информация не найдена*

Бот еще не настроен. Используйте меню для добавления информации.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
		return
	}

	// Check if config is properly set
	isConfigured := cfg.WBToken != "" && cfg.WBToken != "not_set" &&
		cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" &&
		cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!"

	status := "✅ Активен"
	if !isConfigured {
		status = "⚠️ Не полностью настроен"
	} else {
		b.svcMu.RLock()
		svc := b.services[chatID]
		b.svcMu.RUnlock()
		if svc == nil {
			status = "⚠️ Не инициализирован"
		}
	}

	// Helper function to safely truncate UTF-8 strings
	truncateUTF8 := func(s string, maxLen int) string {
		if !utf8.ValidString(s) {
			// If string is not valid UTF-8, return empty
			return ""
		}
		runes := []rune(s)
		if len(runes) <= maxLen {
			return s
		}
		return string(runes[:maxLen]) + "..."
	}

	// Helper function to escape Markdown special characters
	escapeMarkdown := func(s string) string {
		// Escape special Markdown characters: * _ ` [ ] ( ) ~ > # + - | { } . !
		replacer := strings.NewReplacer(
			"*", "\\*",
			"_", "\\_",
			"`", "\\`",
			"[", "\\[",
			"]", "\\]",
			"(", "\\(",
			")", "\\)",
			"~", "\\~",
			">", "\\>",
			"#", "\\#",
			"+", "\\+",
			"-", "\\-",
			"|", "\\|",
			"{", "\\{",
			"}", "\\}",
			".", "\\.",
			"!", "\\!",
		)
		return replacer.Replace(s)
	}

	// Truncate token for display (safely handle UTF-8)
	tokenDisplay := cfg.WBToken
	if tokenDisplay == "not_set" || tokenDisplay == "" {
		tokenDisplay = "❌ Не установлен"
	} else {
		tokenDisplay = truncateUTF8(tokenDisplay, 30)
		// Don't escape token as it's in code block
	}

	// Truncate templates for display (safely handle UTF-8 and escape Markdown)
	templateGoodDisplay := cfg.TemplateGood
	if templateGoodDisplay == "Спасибо за ваш отзыв!" {
		templateGoodDisplay = "⚠️ Не установлен"
	} else {
		templateGoodDisplay = truncateUTF8(templateGoodDisplay, 100)
		// Escape Markdown special characters for safe display
		templateGoodDisplay = escapeMarkdown(templateGoodDisplay)
	}

	templateBadDisplay := cfg.TemplateBad
	if templateBadDisplay == "Спасибо за ваш отзыв!" {
		templateBadDisplay = "⚠️ Не установлен"
	} else {
		templateBadDisplay = truncateUTF8(templateBadDisplay, 100)
		// Escape Markdown special characters for safe display
		templateBadDisplay = escapeMarkdown(templateBadDisplay)
	}

	msg := fmt.Sprintf("📋 *Ваша информация*\n\n"+
		"*Маркетплейс:* Wildberries\n"+
		"*Статус:* %s\n"+
		"*База данных:* SQLite\n\n"+
		"*Токен Wildberries:*\n`%s`\n\n"+
		"*Шаблон для положительных отзывов (4-5 ⭐):*\n"+
		"_%d символов_\n"+
		"`%s`\n\n"+
		"*Шаблон для отрицательных отзывов (1-3 ⭐):*\n"+
		"_%d символов_\n"+
		"`%s`\n\n"+
		"*Обновлено:* %s",
		status,
		tokenDisplay,
		len(cfg.TemplateGood),
		templateGoodDisplay,
		len(cfg.TemplateBad),
		templateBadDisplay,
		cfg.UpdatedAt.Format("02.01.2006 15:04"))

	b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
}

// handleAdminCommand handles /admin command - shows statistics
func (b *Bot) handleAdminCommand(chatID int64, ctx context.Context) {
	// Check if user is admin
	if b.adminUserID == 0 {
		b.log.Warnw("admin command called but admin not configured",
			"chat_id", chatID,
			"admin_user_id", b.adminUserID,
			"tip", "Set ADMIN_USER_ID environment variable and restart bot")
		b.SendMessage(chatID, "❌ *Команда недоступна*\n\nАдминистративная панель не настроена.\n\nУстановите переменную окружения `ADMIN_USER_ID` для включения и перезапустите бота.")
		return
	}

	b.log.Infow("admin command called",
		"chat_id", chatID,
		"admin_user_id", b.adminUserID,
		"is_authorized", chatID == b.adminUserID)

	if chatID != b.adminUserID {
		b.log.Warnw("unauthorized admin access attempt",
			"chat_id", chatID,
			"admin_id", b.adminUserID)
		b.SendMessage(chatID, "❌ *Доступ запрещен*\n\nУ вас нет прав администратора.")
		return
	}

	// Use context with timeout for DB query
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get statistics
	stats, err := b.configStore.GetStats(dbCtx)
	if err != nil {
		b.log.Errorw("failed to get stats", "chat_id", chatID, "err", err)
		metrics.IncrementDatabaseError("get_stats")
		b.SendMessage(chatID, "❌ *Ошибка при получении статистики*\n\nПопробуйте позже.")
		return
	}

	// Get active users count (from services map)
	b.svcMu.RLock()
	activeUsersCount := len(b.services)
	b.svcMu.RUnlock()

	// Format statistics message
	msg := fmt.Sprintf(`🔐 *Административная панель*

📊 *Статистика:*

👥 Всего пользователей в боте: *%d*
🚀 Активных пользователей: *%d*

*Активный пользователь* — это пользователь с настроенным и запущенным сервисом обработки отзывов.`, stats.TotalUsers, activeUsersCount)

	b.SendMessage(chatID, msg)
}

func (b *Bot) handleAddTokenButton(chatID int64) {
	// Check if token already exists
	// Use context with timeout for DB query
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, _ := b.configStore.GetUserConfig(dbCtx, chatID)
	if cfg != nil && cfg.WBToken != "" && cfg.WBToken != "not_set" {
		// Token already exists - show info
		tokenDisplay := cfg.WBToken
		if len(tokenDisplay) > 20 {
			tokenDisplay = tokenDisplay[:20] + "..."
		}
		msg := fmt.Sprintf(`✅ *Токен Wildberries уже настроен*

Токен: %s

Если хотите изменить токен, сначала удалите все данные и начните заново.`, tokenDisplay)
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenuForUser(chatID))
		return
	}

	// Show form for WB token input
	b.setUserState(chatID, StateWaitingToken)
	msg := `🔑 *Добавление токена Wildberries*

Отправьте токен доступа к API Wildberries.

Токен должен иметь право «Отзывы и вопросы» (бит 7).
Получить токен можно в личном кабинете продавца Wildberries.`
	b.SendMessageWithKeyboard(chatID, msg, b.CreateCancelKeyboard())
}

func (b *Bot) handleAddTemplateGoodButton(chatID int64) {
	// Check if token is set
	// Use context with timeout for DB query
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, _ := b.configStore.GetUserConfig(dbCtx, chatID)
	if cfg == nil || cfg.WBToken == "" || cfg.WBToken == "not_set" {
		msg := `⚠️ *Сначала добавьте токен*

Для добавления шаблонов сначала необходимо добавить токен Wildberries.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenuForUser(chatID))
		return
	}

	b.setUserState(chatID, StateWaitingTemplateGood)
	// Try to load existing config
	existingDbCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	existing, _ := b.configStore.GetUserConfig(existingDbCtx, chatID)
	if existing != nil {
		cfg = existing
	}
	b.setUserConfig(chatID, cfg)

	msg := `✅ *Добавление ответа для положительных отзывов*

Отправьте текст ответа для *положительных* отзывов (4-5 звезд).

*Пример:*
"Спасибо за ваш отзыв и доверие к нашему магазину! Нам очень важно, что вы делитесь своим опытом это помогает нам становиться лучше."`

	b.SendMessageWithKeyboard(chatID, msg, b.CreateCancelKeyboard())
}

func (b *Bot) handleAddTemplateBadButton(chatID int64) {
	// Check if token is set
	// Use context with timeout for DB query
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, _ := b.configStore.GetUserConfig(dbCtx, chatID)
	if cfg == nil || cfg.WBToken == "" || cfg.WBToken == "not_set" {
		msg := `⚠️ *Сначала добавьте токен*

Для добавления шаблонов сначала необходимо добавить токен Wildberries.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenuForUser(chatID))
		return
	}

	b.setUserState(chatID, StateWaitingTemplateBad)
	// Try to load existing config
	existingDbCtx2, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	existing, _ := b.configStore.GetUserConfig(existingDbCtx2, chatID)
	if existing != nil {
		cfg = existing
	}
	b.setUserConfig(chatID, cfg)

	msg := `❌ *Добавление ответа для отрицательных отзывов*

Отправьте текст ответа для *отрицательных* отзывов (1-3 звезды).

*Пример:*
"Здравствуйте! Сожалеем, что товар не оправдал ожиданий. У вас есть инструкция, как связаться с нами. Напишите, поможем решить вашу проблему!"`

	b.SendMessageWithKeyboard(chatID, msg, b.CreateCancelKeyboard())
}

func (b *Bot) handleDeleteAllButton(chatID int64) {
	msg := `⚠️ *ВНИМАНИЕ!*

Вы уверены, что хотите удалить ВСЮ информацию?

Это действие нельзя отменить!
Будут удалены:
• Токен Wildberries
• Шаблон для положительных отзывов
• Шаблон для отрицательных отзывов`

	b.SendMessageWithKeyboard(chatID, msg, b.CreateConfirmDeleteKeyboard())
}

func (b *Bot) handleConfirmDelete(chatID int64, ctx context.Context) {
	b.log.Infow("handleConfirmDelete called", "chat_id", chatID)

	err := b.configStore.DeleteUserConfig(ctx, chatID)
	if err != nil {
		b.log.Errorw("failed to delete user config from DB", "chat_id", chatID, "err", err)
		// Try to send error message
		errMsg := tgbotapi.NewMessage(chatID, "Ошибка при удалении информации. Попробуйте позже.")
		b.api.Send(errMsg)
		return
	}

	b.log.Infow("config deleted from DB", "chat_id", chatID)

	// Shutdown user's service and scheduler
	b.log.Infow("calling shutdownUserService", "chat_id", chatID)
	b.shutdownUserService(chatID)
	b.log.Infow("shutdownUserService returned", "chat_id", chatID)

	b.resetUserState(chatID)
	b.log.Infow("state reset", "chat_id", chatID)

	b.log.Infow("starting to send confirmation message", "chat_id", chatID)

	// Try multiple times to send the message
	msg := "Вся информация удалена. Все данные успешно удалены из базы данных. Сервис остановлен. Используйте меню для добавления новой информации."

	// First try: with keyboard
	if err := b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu()); err != nil {
		b.log.Errorw("failed to send delete confirmation with keyboard", "chat_id", chatID, "err", err)

		// Second try: simple message without keyboard
		simpleMsg := tgbotapi.NewMessage(chatID, msg)
		if _, err2 := b.api.Send(simpleMsg); err2 != nil {
			b.log.Errorw("failed to send simple delete confirmation", "chat_id", chatID, "err", err2)

			// Third try: minimal message
			minMsg := tgbotapi.NewMessage(chatID, "Информация удалена.")
			if _, err3 := b.api.Send(minMsg); err3 != nil {
				b.log.Errorw("CRITICAL: failed to send any delete confirmation", "chat_id", chatID, "err", err3)
			} else {
				b.log.Infow("minimal delete confirmation sent", "chat_id", chatID)
			}
		} else {
			b.log.Infow("simple delete confirmation sent", "chat_id", chatID)
		}
	} else {
		b.log.Infow("config deleted successfully with full message", "chat_id", chatID)
	}
}

func (b *Bot) handleCancel(chatID int64) {
	b.resetUserState(chatID)
	b.SendMessageWithKeyboard(chatID, "❌ Действие отменено.", b.CreateMainMenu())
}

func (b *Bot) handleTokenInput(chatID int64, token string, ctx context.Context) {
	token = strings.TrimSpace(token)
	if token == "" {
		b.SendMessageWithKeyboard(chatID, "❌ Токен не может быть пустым. Отправьте корректный токен.", b.CreateCancelKeyboard())
		return
	}

	// Validate token length
	if len(token) < MinTokenLength {
		b.SendMessageWithKeyboard(chatID, fmt.Sprintf("⚠️ Токен слишком короткий. Минимальная длина: %d символов.", MinTokenLength), b.CreateCancelKeyboard())
		return
	}

	if len(token) > MaxTokenLength {
		b.SendMessageWithKeyboard(chatID, fmt.Sprintf("⚠️ Токен слишком длинный. Максимальная длина: %d символов.", MaxTokenLength), b.CreateCancelKeyboard())
		return
	}

	// Validate token format (basic check - alphanumeric and some special chars)
	if !isValidTokenFormat(token) {
		b.SendMessageWithKeyboard(chatID, "⚠️ Токен содержит недопустимые символы. Проверьте правильность токена.", b.CreateCancelKeyboard())
		return
	}

	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
	}

	// Always load existing config from database first
	dbCtxLoad, cancelLoad := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLoad()
	existing, _ := b.configStore.GetUserConfig(dbCtxLoad, chatID)
	if existing != nil {
		cfg.TemplateGood = existing.TemplateGood
		cfg.TemplateBad = existing.TemplateBad
	}

	cfg.WBToken = token
	b.setUserConfig(chatID, cfg)

	// Save to database immediately (with default templates if not set)
	templateGood := cfg.TemplateGood
	templateBad := cfg.TemplateBad
	if templateGood == "" {
		templateGood = "Спасибо за ваш отзыв!"
	}
	if templateBad == "" {
		templateBad = "Спасибо за ваш отзыв!"
	}

	if err := b.configStore.SaveUserConfig(ctx, chatID, token, templateGood, templateBad); err != nil {
		b.log.Errorw("failed to save user config", "chat_id", chatID, "err", err)
		metrics.IncrementDatabaseError("save_config")
		b.SendMessageWithKeyboard(chatID, "❌ Ошибка при сохранении. Попробуйте позже.", b.CreateMainMenu())
		b.resetUserState(chatID)
		return
	}

	// Update in-memory config
	cfg.TemplateGood = templateGood
	cfg.TemplateBad = templateBad
	b.setUserConfig(chatID, cfg)

	// Initialize service if all fields are filled
	allFieldsSet := cfg.WBToken != "" && cfg.WBToken != "not_set" &&
		cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" &&
		cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!"

	if allFieldsSet {
		b.initializeServiceForUser(chatID, cfg, ctx)
		msg := "✅ Токен сохранен!\n\nБот готов к работе. Все необходимые данные настроены."
		if err := b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenuForUser(chatID)); err != nil {
			b.log.Errorw("failed to send token saved message", "chat_id", chatID, "err", err)
			simpleMsg := tgbotapi.NewMessage(chatID, msg)
			b.api.Send(simpleMsg)
		} else {
			b.log.Infow("token saved", "chat_id", chatID)
		}
	} else {
		msg := "✅ Токен сохранен!\n\nТеперь добавьте шаблоны ответов через меню:\n• ✅ Добавить ответ (позитив)\n• ❌ Добавить ответ (негатив)"
		if err := b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenuForUser(chatID)); err != nil {
			b.log.Errorw("failed to send token saved message", "chat_id", chatID, "err", err)
			simpleMsg := tgbotapi.NewMessage(chatID, msg)
			b.api.Send(simpleMsg)
		} else {
			b.log.Infow("token saved", "chat_id", chatID)
		}
	}
	b.resetUserState(chatID)
}

func (b *Bot) handleTemplateGoodInput(chatID int64, text string, ctx context.Context) {
	text = strings.TrimSpace(text)
	if text == "" {
		b.SendMessageWithKeyboard(chatID, "❌ Текст ответа не может быть пустым.", b.CreateCancelKeyboard())
		return
	}

	// Validate template length
	if len([]rune(text)) < 10 {
		b.SendMessageWithKeyboard(chatID, "⚠️ Текст слишком короткий. Рекомендуется минимум 20-30 символов.", b.CreateCancelKeyboard())
		return
	}

	if len([]rune(text)) > MaxTemplateLength {
		b.SendMessageWithKeyboard(chatID, fmt.Sprintf("⚠️ Текст слишком длинный. Максимальная длина: %d символов.", MaxTemplateLength), b.CreateCancelKeyboard())
		return
	}

	// Validate UTF-8 encoding
	if !utf8.ValidString(text) {
		b.SendMessageWithKeyboard(chatID, "❌ Текст содержит некорректные символы. Используйте только допустимые символы.", b.CreateCancelKeyboard())
		return
	}

	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
	}

	// Always load existing config from database first
	dbCtxLoadGood, cancelLoadGood := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLoadGood()
	existing, _ := b.configStore.GetUserConfig(dbCtxLoadGood, chatID)
	if existing != nil {
		cfg.WBToken = existing.WBToken
		cfg.TemplateBad = existing.TemplateBad
	}
	// If existing is nil, cfg will be initialized with empty values

	cfg.TemplateGood = text
	b.setUserConfig(chatID, cfg)

	// Save to database immediately
	wbToken := cfg.WBToken
	templateBad := cfg.TemplateBad
	if wbToken == "" {
		wbToken = "not_set"
	}
	if templateBad == "" {
		templateBad = "Спасибо за ваш отзыв!"
	}

	if err := b.configStore.SaveUserConfig(ctx, chatID, wbToken, cfg.TemplateGood, templateBad); err != nil {
		b.log.Errorw("failed to save user config", "chat_id", chatID, "err", err)
		b.SendMessageWithKeyboard(chatID, "❌ Ошибка при сохранении. Попробуйте позже.", b.CreateMainMenu())
		b.resetUserState(chatID)
		return
	}

	// Update in-memory config
	cfg.WBToken = wbToken
	cfg.TemplateBad = templateBad
	b.setUserConfig(chatID, cfg)

	// Initialize service if all fields are filled
	allFieldsSet := cfg.WBToken != "" && cfg.WBToken != "not_set" &&
		cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" &&
		cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!"

	if allFieldsSet {
		b.initializeServiceForUser(chatID, cfg, ctx)
		msg := "✅ Шаблон для положительных отзывов сохранен!\n\nБот готов к работе. Все необходимые данные настроены."

		// Create inline keyboard with "Run Now" button
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🚀 Запустить программу", CallbackRunNow),
			),
		)

		// Try sending with keyboard first
		keyboardMsg := tgbotapi.NewMessage(chatID, msg)
		keyboardMsg.ReplyMarkup = keyboard

		if _, err := b.api.Send(keyboardMsg); err != nil {
			b.log.Errorw("failed to send with keyboard, trying simple message", "chat_id", chatID, "err", err)
			simpleMsg := tgbotapi.NewMessage(chatID, msg)
			b.api.Send(simpleMsg)
		} else {
			b.log.Infow("template good saved with run button", "chat_id", chatID)
		}
	} else {
		msg := "✅ Шаблон для положительных отзывов сохранен!\n\nТеперь добавьте шаблон для отрицательных отзывов через меню."
		if err := b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenuForUser(chatID)); err != nil {
			b.log.Errorw("failed to send template saved message", "chat_id", chatID, "err", err)
			simpleMsg := tgbotapi.NewMessage(chatID, msg)
			b.api.Send(simpleMsg)
		} else {
			b.log.Infow("template good saved", "chat_id", chatID)
		}
	}
	b.resetUserState(chatID)
}

func (b *Bot) handleTemplateBadInput(chatID int64, text string, ctx context.Context) {
	b.log.Infow("handleTemplateBadInput called", "chat_id", chatID, "text_length", len(text))

	text = strings.TrimSpace(text)
	if text == "" {
		b.log.Warnw("empty template text", "chat_id", chatID)
		b.SendMessageWithKeyboard(chatID, "❌ Текст ответа не может быть пустым.", b.CreateCancelKeyboard())
		return
	}

	// Validate template length
	if len([]rune(text)) < 10 {
		b.log.Warnw("template too short", "chat_id", chatID, "length", len([]rune(text)))
		b.SendMessageWithKeyboard(chatID, "⚠️ Текст слишком короткий. Рекомендуется минимум 20-30 символов.", b.CreateCancelKeyboard())
		return
	}

	if len([]rune(text)) > MaxTemplateLength {
		b.log.Warnw("template too long", "chat_id", chatID, "length", len([]rune(text)))
		b.SendMessageWithKeyboard(chatID, fmt.Sprintf("⚠️ Текст слишком длинный. Максимальная длина: %d символов.", MaxTemplateLength), b.CreateCancelKeyboard())
		return
	}

	// Validate UTF-8 encoding
	if !utf8.ValidString(text) {
		b.log.Warnw("invalid UTF-8 in template", "chat_id", chatID)
		b.SendMessageWithKeyboard(chatID, "❌ Текст содержит некорректные символы. Используйте только допустимые символы.", b.CreateCancelKeyboard())
		return
	}

	b.log.Infow("template validation passed", "chat_id", chatID)

	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
	}

	// Always load existing config from database first
	dbCtxLoadBad, cancelLoadBad := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLoadBad()
	existing, _ := b.configStore.GetUserConfig(dbCtxLoadBad, chatID)
	if existing != nil {
		cfg.WBToken = existing.WBToken
		cfg.TemplateGood = existing.TemplateGood
		cfg.TemplateBad = existing.TemplateBad
	}

	cfg.TemplateBad = text
	b.setUserConfig(chatID, cfg)

	// Save to database immediately
	wbToken := cfg.WBToken
	templateGood := cfg.TemplateGood
	if wbToken == "" {
		wbToken = "not_set"
	}
	if templateGood == "" {
		templateGood = "Спасибо за ваш отзыв!"
	}

	b.log.Infow("saving template bad to database", "chat_id", chatID)

	if err := b.configStore.SaveUserConfig(ctx, chatID, wbToken, templateGood, cfg.TemplateBad); err != nil {
		b.log.Errorw("failed to save user config to DB", "chat_id", chatID, "err", err)
		errMsg := tgbotapi.NewMessage(chatID, "Ошибка при сохранении. Попробуйте позже.")
		b.api.Send(errMsg)
		b.resetUserState(chatID)
		return
	}

	b.log.Infow("template bad saved to DB successfully", "chat_id", chatID)

	// Update in-memory config
	cfg.WBToken = wbToken
	cfg.TemplateGood = templateGood
	b.setUserConfig(chatID, cfg)

	// Initialize service if all fields are filled
	allFieldsSet := cfg.WBToken != "" && cfg.WBToken != "not_set" &&
		cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" &&
		cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!"

	b.log.Infow("checking if all fields set", "chat_id", chatID, "all_fields_set", allFieldsSet)

	if allFieldsSet {
		b.log.Infow("all fields set, initializing service", "chat_id", chatID)
		b.initializeServiceForUser(chatID, cfg, ctx)
		b.log.Infow("service initialization completed, preparing message", "chat_id", chatID)

		msg := `✅ Шаблон для отрицательных отзывов сохранен!`

		b.log.Infow("sending completion message", "chat_id", chatID)

		// Create inline keyboard with "Run Now" button
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🚀 Запустить программу", CallbackRunNow),
			),
		)

		// Try sending with keyboard first
		keyboardMsg := tgbotapi.NewMessage(chatID, msg)
		keyboardMsg.ReplyMarkup = keyboard

		if _, err := b.api.Send(keyboardMsg); err != nil {
			// Fallback to simple message without keyboard
			b.log.Errorw("failed to send with keyboard, trying simple message", "chat_id", chatID, "err", err)
			simpleMsg := tgbotapi.NewMessage(chatID, msg)
			if _, err := b.api.Send(simpleMsg); err != nil {
				b.log.Errorw("CRITICAL: failed to send template bad confirmation", "chat_id", chatID, "err", err)
			} else {
				b.log.Infow("template bad confirmation sent successfully (simple)", "chat_id", chatID)
			}
		} else {
			b.log.Infow("template bad confirmation sent successfully with run button", "chat_id", chatID)
		}
	} else {
		b.log.Infow("not all fields set yet", "chat_id", chatID)
		msg := "Шаблон для отрицательных отзывов сохранен! Продолжите настройку через меню."

		simpleMsg := tgbotapi.NewMessage(chatID, msg)
		if _, err := b.api.Send(simpleMsg); err != nil {
			b.log.Errorw("CRITICAL: failed to send template bad confirmation", "chat_id", chatID, "err", err)
		} else {
			b.log.Infow("template bad confirmation sent successfully", "chat_id", chatID)
		}
	}

	b.log.Infow("resetting user state", "chat_id", chatID)
	b.resetUserState(chatID)
}

func (b *Bot) initializeServiceForUser(chatID int64, cfg *storage.UserConfig, ctx context.Context) {
	b.log.Infow("initializeServiceForUser: starting", "chat_id", chatID)

	b.log.Infow("initializeServiceForUser: acquiring lock", "chat_id", chatID)
	b.svcMu.Lock()
	defer func() {
		b.log.Infow("initializeServiceForUser: releasing lock", "chat_id", chatID)
		b.svcMu.Unlock()
	}()
	b.log.Infow("initializeServiceForUser: lock acquired", "chat_id", chatID)

	// Check if service already exists for this user
	if _, exists := b.services[chatID]; exists {
		b.log.Infow("service already exists for user", "chat_id", chatID)
		return
	}

	// Create Wildberries API client for this user
	wbClient := wbapi.New(
		cfg.WBToken,
		wbapi.WithBaseURL(b.wbBaseURL),
		wbapi.WithRateLimit(3, 6),
		wbapi.WithLogger(b.log),
	)
	b.log.Infow("wb client initialized for user", "chat_id", chatID)

	// Create service with user's templates and userID
	const maxTake = 5000
	svc := service.New(
		chatID,
		wbClient,
		b.userStore,
		cfg.TemplateBad,
		cfg.TemplateGood,
		b.log,
		maxTake,
	)

	b.services[chatID] = svc
	b.log.Infow("service initialized for user", "chat_id", chatID)

	// Start scheduler for this user
	// Use b.ctx (bot's main context) instead of request ctx to keep scheduler running
	b.log.Infow("creating scheduler", "chat_id", chatID)
	poller := scheduler.New(10*time.Minute, svc.HandleCycle, b.log)
	b.schedulers[chatID] = poller

	b.log.Infow("starting scheduler goroutine", "chat_id", chatID)
	go poller.Run(b.ctx)
	b.log.Infow("scheduler started for user", "chat_id", chatID, "interval", "10m")

	// Update metrics
	b.log.Infow("updating metrics", "chat_id", chatID)
	go b.updateActiveUsersMetric() // Run async to avoid deadlock
	b.log.Infow("initializeServiceForUser: completed", "chat_id", chatID)
}

func (b *Bot) getServiceForUser(chatID int64) *service.Service {
	b.svcMu.RLock()
	defer b.svcMu.RUnlock()
	return b.services[chatID]
}

func (b *Bot) shutdownUserService(chatID int64) {
	b.svcMu.Lock()
	defer b.svcMu.Unlock()

	if sched, exists := b.schedulers[chatID]; exists {
		sched.Shutdown()
		delete(b.schedulers, chatID)
	}
	delete(b.services, chatID)
	b.log.Infow("service and scheduler stopped for user", "chat_id", chatID)

	// Update metrics (call without holding lock to avoid deadlock)
	go b.updateActiveUsersMetric()
}

// cleanupInactiveUsers periodically cleans up inactive users from maps
// Inactive users are those who haven't used the bot for 24 hours
// and don't have active services
func (b *Bot) cleanupInactiveUsers(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.performCleanup()
		}
	}
}

// performCleanup removes inactive users from maps
func (b *Bot) performCleanup() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.svcMu.RLock()
	activeUserIDs := make(map[int64]bool)
	for chatID := range b.services {
		activeUserIDs[chatID] = true
	}
	b.svcMu.RUnlock()

	// Clean up userStates for users without active services
	for chatID := range b.userStates {
		if !activeUserIDs[chatID] {
			delete(b.userStates, chatID)
		}
	}

	// Clean up userConfig for users without active services
	for chatID := range b.userConfig {
		if !activeUserIDs[chatID] {
			delete(b.userConfig, chatID)
		}
	}

	// Clean up rate limiters for users without active services
	b.rateLimitMu.Lock()
	for chatID := range b.userRateLimiters {
		if !activeUserIDs[chatID] {
			delete(b.userRateLimiters, chatID)
		}
	}
	b.rateLimitMu.Unlock()

	// Clean up subscription cache for users without active services
	b.subscriptionCacheMu.Lock()
	for chatID := range b.subscriptionCache {
		if !activeUserIDs[chatID] {
			delete(b.subscriptionCache, chatID)
		}
	}
	b.subscriptionCacheMu.Unlock()

	b.log.Debugw("cleanup completed",
		"user_states", len(b.userStates),
		"user_configs", len(b.userConfig),
		"rate_limiters", len(b.userRateLimiters),
		"subscription_cache", len(b.subscriptionCache))
}

// updateActiveUsersMetric updates the active users metric
func (b *Bot) updateActiveUsersMetric() {
	b.svcMu.RLock()
	count := len(b.services)
	b.svcMu.RUnlock()
	metrics.UpdateActiveUsers(count)
}

// Shutdown gracefully stops all schedulers and cleans up resources
func (b *Bot) Shutdown() {
	b.log.Info("shutting down bot, stopping all schedulers...")

	b.svcMu.Lock()
	defer b.svcMu.Unlock()

	// Stop all schedulers
	for chatID, sched := range b.schedulers {
		sched.Shutdown()
		b.log.Debugw("scheduler stopped", "chat_id", chatID)
	}

	// Clear maps
	b.schedulers = make(map[int64]*scheduler.Scheduler)
	b.services = make(map[int64]*service.Service)

	b.log.Info("all schedulers stopped")

	// Update metrics
	metrics.UpdateActiveUsers(0)
}

func (b *Bot) handleRunNowButton(chatID int64, ctx context.Context) {
	// Use context with timeout for DB query
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := b.configStore.GetUserConfig(dbCtx, chatID)
	if err != nil || cfg == nil {
		msg := `❌ *Бот не настроен*

Для запуска программы необходимо:
• Добавить токен Wildberries
• Добавить шаблон для положительных отзывов
• Добавить шаблон для отрицательных отзывов

Используйте меню для добавления информации.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
		return
	}

	// Check if config is properly set
	var missingFields []string

	if cfg.WBToken == "" || cfg.WBToken == "not_set" {
		missingFields = append(missingFields, "Wildberries токен")
	}
	if cfg.TemplateGood == "" || cfg.TemplateGood == "Спасибо за ваш отзыв!" {
		missingFields = append(missingFields, "Шаблон для положительных отзывов")
	}
	if cfg.TemplateBad == "" || cfg.TemplateBad == "Спасибо за ваш отзыв!" {
		missingFields = append(missingFields, "Шаблон для отрицательных отзывов")
	}

	isProperlyConfigured := cfg.WBToken != "" && cfg.WBToken != "not_set" &&
		cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" &&
		cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!"

	if !isProperlyConfigured {
		msg := fmt.Sprintf(`❌ *Бот не полностью настроен*

Для запуска программы необходимо:
• Токен Wildberries
• Шаблон для положительных отзывов
• Шаблон для отрицательных отзывов

Отсутствует: %s

Используйте кнопку "📋 Информация" для проверки текущих настроек.`,
			strings.Join(missingFields, ", "))
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
		return
	}

	// Get or initialize service for this user
	svc := b.getServiceForUser(chatID)
	if svc == nil {
		b.initializeServiceForUser(chatID, cfg, ctx)
		svc = b.getServiceForUser(chatID)
	}

	if svc == nil {
		msg := `❌ *Сервис не инициализирован*

Проверьте правильность введенных данных и попробуйте снова.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
		return
	}

	// Send immediate feedback
	msg := "🚀 Запуск обработки отзывов\n\nБот начал обрабатывать отзывы на Wildberries.\nЭто может занять некоторое время..."

	if err := b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu()); err != nil {
		b.log.Errorw("failed to send run confirmation", "chat_id", chatID, "err", err)
		// Fallback
		simpleMsg := tgbotapi.NewMessage(chatID, msg)
		b.api.Send(simpleMsg)
	} else {
		b.log.Infow("run now started", "chat_id", chatID)
	}

	// Run in background
	go func() {
		// Panic recovery
		defer func() {
			if r := recover(); r != nil {
				b.log.Errorw("panic recovered in handleRunNowButton cycle",
					"chat_id", chatID,
					"panic", r)
			}
		}()

		// Use background context for cycle execution
		cycleCtx := context.Background()
		b.log.Infow("manual cycle triggered via telegram button", "chat_id", chatID)
		svc.HandleCycle(cycleCtx)

		// Send completion message
		completionMsg := "✅ Обработка завершена\n\nБот завершил обработку отзывов.\nПроверьте результаты в личном кабинете Wildberries.\n\nДля повторного запуска используйте кнопку \"🚀 Запустить программу\""

		if err := b.SendMessage(chatID, completionMsg); err != nil {
			b.log.Errorw("failed to send completion message", "chat_id", chatID, "err", err)
		}
	}()
}

func (b *Bot) handleRunNow(chatID int64, ctx context.Context) {
	b.handleRunNowButton(chatID, ctx)
}

func (b *Bot) handleCheckSubscription(chatID int64) {
	// Invalidate cache for this user to force fresh check
	b.subscriptionCacheMu.Lock()
	delete(b.subscriptionCache, chatID)
	b.subscriptionCacheMu.Unlock()

	// Now check subscription (will make API call)
	if b.checkChannelSubscription(chatID) {
		msg := `✅ *Подписка подтверждена!*

Добро пожаловать! Теперь вы можете использовать все функции бота.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
	} else {
		b.sendChannelSubscriptionMessage(chatID)
	}
}

// State management helpers
func (b *Bot) getUserState(chatID int64) UserState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.userStates[chatID]
}

func (b *Bot) setUserState(chatID int64, state UserState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.userStates[chatID] = state
}

func (b *Bot) resetUserState(chatID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.userStates, chatID)
	delete(b.userConfig, chatID)
}

func (b *Bot) getUserConfig(chatID int64) *storage.UserConfig {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.userConfig[chatID]
}

func (b *Bot) setUserConfig(chatID int64, cfg *storage.UserConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.userConfig[chatID] = cfg
}
