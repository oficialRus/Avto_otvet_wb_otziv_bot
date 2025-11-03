package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"feedback_bot/internal/scheduler"
	"feedback_bot/internal/service"
	"feedback_bot/internal/storage"
	"feedback_bot/internal/wbapi"
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
	CallbackMainMenu       = "main_menu"
	CallbackAddToken       = "add_token"
	CallbackAddTemplateGood = "add_template_good"
	CallbackAddTemplateBad  = "add_template_bad"
	CallbackViewInfo       = "view_info"
	CallbackDeleteAll      = "delete_all"
	CallbackCancel         = "cancel"
	CallbackConfirmDelete  = "confirm_delete"
	CallbackRunNow         = "run_now"
)

// Bot handles Telegram commands and configuration flow.
type Bot struct {
	api         *tgbotapi.BotAPI
	service     *service.Service
	log         *zap.SugaredLogger
	ctx         context.Context
	configStore storage.ConfigStore
	userStore   storage.Store

	// User states for configuration flow
	userStates map[int64]UserState
	userConfig map[int64]*storage.UserConfig // Temporary storage during setup
	mu         sync.RWMutex

	// Service creation dependencies
	wbBaseURL     string
	pollInterval  string

	// Scheduler for automatic processing
	scheduler        *scheduler.Scheduler
	schedulerStarted bool
}

// New creates a new Telegram bot instance.
// Telegram token is now required.
func New(token string, configStore storage.ConfigStore, userStore storage.Store, logger *zap.SugaredLogger, ctx context.Context) (*Bot, error) {
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

	bot := &Bot{
		api:              api,
		log:              logger,
		ctx:              ctx,
		configStore:      configStore,
		userStore:        userStore,
		userStates:       make(map[int64]UserState),
		userConfig:       make(map[int64]*storage.UserConfig),
		wbBaseURL:        "https://feedbacks-api.wildberries.ru",
		pollInterval:     "10m",
		schedulerStarted: false,
	}

	bot.log.Infow("telegram bot authorized", "username", api.Self.UserName)
	return bot, nil
}

// Run starts the bot's update loop. It blocks until context is cancelled.
func (b *Bot) Run(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	b.log.Info("telegram bot started, waiting for commands")

	for {
		select {
		case <-ctx.Done():
			b.log.Info("telegram bot: context cancelled, stopping")
			b.api.StopReceivingUpdates()
			return
		case update := <-updates:
			if update.CallbackQuery != nil {
				go b.handleCallbackQuery(ctx, update.CallbackQuery)
			} else if update.Message != nil {
				go b.handleMessage(ctx, update.Message)
			}
		}
	}
}

// SendMessage sends a message to the specified chat ID.
func (b *Bot) SendMessage(chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	_, err := b.api.Send(msg)
	if err != nil {
		b.log.Warnw("failed to send telegram message", "chat_id", chatID, "err", err)
		return err
	}
	return nil
}

// SendMessageWithKeyboard sends a message with inline keyboard
func (b *Bot) SendMessageWithKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) error {
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
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Информация", CallbackViewInfo),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 Запустить программу", CallbackRunNow),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔑 Добавить ТОКЕН", CallbackAddToken),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Добавить ответ (позитив)", CallbackAddTemplateGood),
			tgbotapi.NewInlineKeyboardButtonData("❌ Добавить ответ (негатив)", CallbackAddTemplateBad),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 СТЕРЕТЬ ВСЮ ИНФОРМАЦИЮ", CallbackDeleteAll),
		),
	)
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

	b.log.Debugw("received callback query", "chat_id", chatID, "data", data)

	switch data {
	case CallbackMainMenu:
		b.showMainMenu(chatID)
	case CallbackViewInfo:
		b.handleViewInfo(chatID, ctx)
	case CallbackAddToken:
		b.handleAddTokenButton(chatID)
	case CallbackAddTemplateGood:
		b.handleAddTemplateGoodButton(chatID)
	case CallbackAddTemplateBad:
		b.handleAddTemplateBadButton(chatID)
	case CallbackDeleteAll:
		b.handleDeleteAllButton(chatID)
	case CallbackConfirmDelete:
		b.handleConfirmDelete(chatID, ctx)
	case CallbackCancel:
		b.handleCancel(chatID)
	case CallbackRunNow:
		b.handleRunNowButton(chatID, ctx)
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

	b.log.Debugw("received telegram message", "chat_id", chatID, "command", command)

	// Handle commands
	if strings.HasPrefix(command, "/") {
		switch {
		case command == "/start" || command == "/help":
			b.showMainMenu(chatID)
			return
		case command == "/status":
			b.handleViewInfo(chatID, ctx)
			return
		case command == "/run" || command == "/run_now":
			b.handleRunNow(chatID, ctx)
			return
		}
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
	cfg, _ := b.configStore.GetUserConfig(b.ctx, chatID)
	
	msg := `🤖 *Автоответчик на отзывы Wildberries*

Выберите действие из меню:`

	if cfg != nil {
		msg += "\n\n✅ Бот настроен и работает!"
	} else {
		msg += "\n\n⚠️ Бот не настроен. Добавьте необходимую информацию."
	}

	b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
}

func (b *Bot) handleViewInfo(chatID int64, ctx context.Context) {
	cfg, err := b.configStore.GetUserConfig(ctx, chatID)
	if err != nil || cfg == nil {
		msg := `❌ *Информация не найдена*

Бот еще не настроен. Используйте меню для добавления информации.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
		return
	}

	// Check if config is properly set (not using defaults)
	isConfigured := cfg.WBToken != "" && cfg.WBToken != "not_set" &&
		cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" &&
		cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!"

	status := "✅ Активен"
	if !isConfigured {
		status = "⚠️ Не полностью настроен"
	} else if b.service == nil {
		status = "⚠️ Не инициализирован"
	}

	// Truncate token for display
	tokenDisplay := cfg.WBToken
	if tokenDisplay == "not_set" {
		tokenDisplay = "❌ Не установлен"
	} else if len(tokenDisplay) > 30 {
		tokenDisplay = tokenDisplay[:30] + "..."
	}

	// Truncate templates for display (show first 100 chars)
	templateGoodDisplay := cfg.TemplateGood
	if templateGoodDisplay == "Спасибо за ваш отзыв!" {
		templateGoodDisplay = "⚠️ Не установлен"
	} else if len(templateGoodDisplay) > 100 {
		templateGoodDisplay = templateGoodDisplay[:100] + "..."
	}

	templateBadDisplay := cfg.TemplateBad
	if templateBadDisplay == "Спасибо за ваш отзыв!" {
		templateBadDisplay = "⚠️ Не установлен"
	} else if len(templateBadDisplay) > 100 {
		templateBadDisplay = templateBadDisplay[:100] + "..."
	}

	msg := fmt.Sprintf("📋 *Ваша информация*\n\n"+
		"*Статус:* %s\n"+
		"*База данных:* SQLite\n\n"+
		"*Токен Wildberries:*\n"+
		"`%s`\n\n"+
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

func (b *Bot) handleAddTokenButton(chatID int64) {
	b.setUserState(chatID, StateWaitingToken)
	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
		// Try to load existing config
		existing, _ := b.configStore.GetUserConfig(b.ctx, chatID)
		if existing != nil {
			cfg.WBToken = existing.WBToken
			cfg.TemplateGood = existing.TemplateGood
			cfg.TemplateBad = existing.TemplateBad
		}
		b.setUserConfig(chatID, cfg)
	} else {
		// Reload from database to ensure we have latest data
		existing, _ := b.configStore.GetUserConfig(b.ctx, chatID)
		if existing != nil {
			cfg.WBToken = existing.WBToken
			cfg.TemplateGood = existing.TemplateGood
			cfg.TemplateBad = existing.TemplateBad
			b.setUserConfig(chatID, cfg)
		}
	}

	msg := `🔑 *Добавление токена*

Отправьте токен доступа к API Wildberries.

Токен должен иметь право «Отзывы и вопросы» (бит 7).
Получить токен можно в личном кабинете продавца Wildberries.`

	b.SendMessageWithKeyboard(chatID, msg, b.CreateCancelKeyboard())
}

func (b *Bot) handleAddTemplateGoodButton(chatID int64) {
	b.setUserState(chatID, StateWaitingTemplateGood)
	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
		// Try to load existing config
		existing, _ := b.configStore.GetUserConfig(b.ctx, chatID)
		if existing != nil {
			cfg.WBToken = existing.WBToken
			cfg.TemplateGood = existing.TemplateGood
			cfg.TemplateBad = existing.TemplateBad
		}
		b.setUserConfig(chatID, cfg)
	} else {
		// Reload from database to ensure we have latest data
		existing, _ := b.configStore.GetUserConfig(b.ctx, chatID)
		if existing != nil {
			cfg.WBToken = existing.WBToken
			cfg.TemplateGood = existing.TemplateGood
			cfg.TemplateBad = existing.TemplateBad
			b.setUserConfig(chatID, cfg)
		}
	}

	msg := `✅ *Добавление ответа для положительных отзывов*

Отправьте текст ответа для *положительных* отзывов (4-5 звезд).

*Пример:*
"Спасибо за ваш отзыв и доверие к нашему магазину! Нам очень важно, что вы делитесь своим опытом это помогает нам становиться лучше."`

	b.SendMessageWithKeyboard(chatID, msg, b.CreateCancelKeyboard())
}

func (b *Bot) handleAddTemplateBadButton(chatID int64) {
	b.setUserState(chatID, StateWaitingTemplateBad)
	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
		// Try to load existing config
		existing, _ := b.configStore.GetUserConfig(b.ctx, chatID)
		if existing != nil {
			cfg.WBToken = existing.WBToken
			cfg.TemplateGood = existing.TemplateGood
			cfg.TemplateBad = existing.TemplateBad
		}
		b.setUserConfig(chatID, cfg)
	} else {
		// Reload from database to ensure we have latest data
		existing, _ := b.configStore.GetUserConfig(b.ctx, chatID)
		if existing != nil {
			cfg.WBToken = existing.WBToken
			cfg.TemplateGood = existing.TemplateGood
			cfg.TemplateBad = existing.TemplateBad
			b.setUserConfig(chatID, cfg)
		}
	}

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
	err := b.configStore.DeleteUserConfig(ctx, chatID)
	if err != nil {
		b.log.Errorw("failed to delete user config", "chat_id", chatID, "err", err)
		b.SendMessage(chatID, "❌ Ошибка при удалении информации. Попробуйте позже.")
		return
	}

	// Reset service
	b.service = nil
	b.schedulerStarted = false
	if b.scheduler != nil {
		b.scheduler.Shutdown()
		b.scheduler = nil
	}

	b.resetUserState(chatID)

	msg := `✅ *Вся информация удалена*

Все данные успешно удалены из базы данных.

Используйте меню для добавления новой информации.`

	b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
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

	if len(token) < 20 {
		b.SendMessageWithKeyboard(chatID, "⚠️ Токен кажется слишком коротким. Убедитесь, что скопировали полный токен.", b.CreateCancelKeyboard())
		return
	}

	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
	}
	
	// Always load existing config from database first
	existing, _ := b.configStore.GetUserConfig(ctx, chatID)
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

	if err := b.configStore.SaveUserConfig(ctx, chatID, cfg.WBToken, templateGood, templateBad); err != nil {
		b.log.Errorw("failed to save user config", "chat_id", chatID, "err", err)
		b.SendMessageWithKeyboard(chatID, "❌ Ошибка при сохранении. Попробуйте позже.", b.CreateMainMenu())
		b.resetUserState(chatID)
		return
	}

	// Update in-memory config
	cfg.TemplateGood = templateGood
	cfg.TemplateBad = templateBad
	b.setUserConfig(chatID, cfg)

	// Initialize service if all fields are filled
	if cfg.TemplateGood != "" && cfg.TemplateBad != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" && cfg.TemplateBad != "Спасибо за ваш отзыв!" {
		b.initializeServiceForUser(chatID, cfg, ctx)
		msg := `✅ *Токен сохранен!*

Бот готов к работе. Все необходимые данные настроены.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
	} else {
		msg := `✅ *Токен сохранен!*

Теперь добавьте шаблоны ответов через меню.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
	}
	b.resetUserState(chatID)
}

func (b *Bot) handleTemplateGoodInput(chatID int64, text string, ctx context.Context) {
	text = strings.TrimSpace(text)
	if text == "" {
		b.SendMessageWithKeyboard(chatID, "❌ Текст ответа не может быть пустым.", b.CreateCancelKeyboard())
		return
	}

	if len(text) < 10 {
		b.SendMessageWithKeyboard(chatID, "⚠️ Текст слишком короткий. Рекомендуется минимум 20-30 символов.", b.CreateCancelKeyboard())
		return
	}

	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
	}
	
	// Always load existing config from database first
	existing, _ := b.configStore.GetUserConfig(ctx, chatID)
	if existing != nil {
		cfg.WBToken = existing.WBToken
		cfg.TemplateBad = existing.TemplateBad
	}
	
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
	if cfg.WBToken != "" && cfg.WBToken != "not_set" && cfg.TemplateBad != "" && cfg.TemplateBad != "Спасибо за ваш отзыв!" {
		b.initializeServiceForUser(chatID, cfg, ctx)
		msg := `✅ *Шаблон для положительных отзывов сохранен!*

Бот готов к работе. Все необходимые данные настроены.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
	} else {
		msg := `✅ *Шаблон для положительных отзывов сохранен!*

Продолжите настройку через меню.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
	}
	b.resetUserState(chatID)
}

func (b *Bot) handleTemplateBadInput(chatID int64, text string, ctx context.Context) {
	text = strings.TrimSpace(text)
	if text == "" {
		b.SendMessageWithKeyboard(chatID, "❌ Текст ответа не может быть пустым.", b.CreateCancelKeyboard())
		return
	}

	if len(text) < 10 {
		b.SendMessageWithKeyboard(chatID, "⚠️ Текст слишком короткий. Рекомендуется минимум 20-30 символов.", b.CreateCancelKeyboard())
		return
	}

	cfg := b.getUserConfig(chatID)
	if cfg == nil {
		cfg = &storage.UserConfig{UserID: chatID}
	}
	
	// Always load existing config from database first
	existing, _ := b.configStore.GetUserConfig(ctx, chatID)
	if existing != nil {
		cfg.WBToken = existing.WBToken
		cfg.TemplateGood = existing.TemplateGood
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

	if err := b.configStore.SaveUserConfig(ctx, chatID, wbToken, templateGood, cfg.TemplateBad); err != nil {
		b.log.Errorw("failed to save user config", "chat_id", chatID, "err", err)
		b.SendMessageWithKeyboard(chatID, "❌ Ошибка при сохранении. Попробуйте позже.", b.CreateMainMenu())
		b.resetUserState(chatID)
		return
	}

	// Update in-memory config
	cfg.WBToken = wbToken
	cfg.TemplateGood = templateGood
	b.setUserConfig(chatID, cfg)

	// Initialize service if all fields are filled
	if cfg.WBToken != "" && cfg.WBToken != "not_set" && cfg.TemplateGood != "" && cfg.TemplateGood != "Спасибо за ваш отзыв!" {
		b.initializeServiceForUser(chatID, cfg, ctx)
		msg := `✅ *Шаблон для отрицательных отзывов сохранен!*

🎉 *Настройка завершена!*

Бот готов к работе. Все необходимые данные настроены.
Бот будет автоматически обрабатывать отзывы каждые 10 минут.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
	} else {
		msg := `✅ *Шаблон для отрицательных отзывов сохранен!*

Продолжите настройку через меню.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
	}
	b.resetUserState(chatID)
}

func (b *Bot) initializeServiceForUser(chatID int64, cfg *storage.UserConfig, ctx context.Context) {
	// Create WB API client for this user
	wbClient := wbapi.New(
		cfg.WBToken,
		wbapi.WithBaseURL(b.wbBaseURL),
		wbapi.WithRateLimit(3, 6),
		wbapi.WithLogger(b.log),
	)

	// Create service with user's templates
	const maxTake = 5000
	svc := service.New(
		wbClient,
		b.userStore,
		cfg.TemplateBad,
		cfg.TemplateGood,
		b.log,
		maxTake,
	)

	b.service = svc
	b.log.Infow("service initialized for user", "chat_id", chatID)

	// Start scheduler in background if not already started
	if !b.schedulerStarted {
		b.log.Info("starting scheduler for automatic feedback processing")
		poller := scheduler.New(10*time.Minute, svc.HandleCycle, b.log)
		b.scheduler = poller
		go poller.Run(ctx)
		b.schedulerStarted = true
		b.log.Info("scheduler started - automatic processing enabled")
	} else {
		b.log.Info("scheduler already running, service updated")
	}
}

func (b *Bot) handleRunNowButton(chatID int64, ctx context.Context) {
	cfg, err := b.configStore.GetUserConfig(ctx, chatID)
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
	if cfg.WBToken == "" || cfg.WBToken == "not_set" ||
		cfg.TemplateGood == "" || cfg.TemplateGood == "Спасибо за ваш отзыв!" ||
		cfg.TemplateBad == "" || cfg.TemplateBad == "Спасибо за ваш отзыв!" {
		msg := `❌ *Бот не полностью настроен*

Для запуска программы необходимо:
• Добавить токен Wildberries
• Добавить шаблон для положительных отзывов
• Добавить шаблон для отрицательных отзывов

Используйте кнопку "📋 Информация" для проверки текущих настроек.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
		return
	}

	if b.service == nil {
		b.initializeServiceForUser(chatID, cfg, ctx)
	}

	if b.service == nil {
		msg := `❌ *Сервис не инициализирован*

Проверьте правильность введенных данных и попробуйте снова.`
		b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())
		return
	}

	// Send immediate feedback
	msg := `🚀 *Запуск обработки отзывов*

Бот начал обрабатывать отзывы на Wildberries.
Это может занять некоторое время...`
	b.SendMessageWithKeyboard(chatID, msg, b.CreateMainMenu())

	// Run in background
	go func() {
		b.log.Infow("manual cycle triggered via telegram button", "chat_id", chatID)
		b.service.HandleCycle(ctx)
		
		// Send completion message
		b.SendMessage(chatID, `✅ *Обработка завершена*

Бот завершил обработку отзывов.
Проверьте результаты в личном кабинете Wildberries.

Для повторного запуска используйте кнопку "🚀 Запустить программу"`)
	}()
}

func (b *Bot) handleRunNow(chatID int64, ctx context.Context) {
	b.handleRunNowButton(chatID, ctx)
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
