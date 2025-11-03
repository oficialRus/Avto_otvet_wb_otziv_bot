package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"feedback_bot/internal/service"
)

// Bot handles Telegram commands and notifications for the feedback service.
type Bot struct {
	api     *tgbotapi.BotAPI
	service *service.Service
	log     *zap.SugaredLogger
	ctx     context.Context
}

// New creates a new Telegram bot instance.
// If token is empty, returns nil (bot is optional).
func New(token string, svc *service.Service, logger *zap.SugaredLogger, ctx context.Context) (*Bot, error) {
	if token == "" {
		return nil, nil // bot is optional
	}

	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	if logger == nil {
		logger = zap.NewNop().Sugar()
	}

	bot := &Bot{
		api:     api,
		service: svc,
		log:     logger,
		ctx:     ctx,
	}

	bot.log.Infow("telegram bot authorized", "username", api.Self.UserName)
	return bot, nil
}

// Run starts the bot's update loop. It blocks until context is cancelled.
func (b *Bot) Run(ctx context.Context) {
	if b == nil {
		return
	}

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
			if update.Message == nil {
				continue
			}
			go b.handleMessage(ctx, update.Message)
		}
	}
}

// SendMessage sends a message to the specified chat ID.
func (b *Bot) SendMessage(chatID int64, text string) error {
	if b == nil {
		return nil
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	_, err := b.api.Send(msg)
	if err != nil {
		b.log.Warnw("failed to send telegram message", "chat_id", chatID, "err", err)
		return err
	}
	return nil
}

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	if msg == nil || msg.Text == "" {
		return
	}

	command := strings.ToLower(strings.TrimSpace(msg.Text))
	chatID := msg.Chat.ID

	b.log.Debugw("received telegram command", "chat_id", chatID, "command", command)

	var response string
	switch {
	case command == "/start" || command == "/help":
		response = b.handleHelp()
	case command == "/status":
		response = b.handleStatus()
	case command == "/run" || command == "/run_now":
		response = b.handleRunNow(ctx)
	default:
		response = "❓ Неизвестная команда. Используйте /help для списка команд."
	}

	if err := b.SendMessage(chatID, response); err != nil {
		b.log.Warnw("failed to send response", "chat_id", chatID, "err", err)
	}
}

func (b *Bot) handleHelp() string {
	return `🤖 *Автоответчик на отзывы Wildberries*

*Доступные команды:*

/start, /help — показать это сообщение
/status — показать статус сервиса
/run, /run_now — запустить цикл обработки отзывов вручную

*Информация:*
Сервис автоматически обрабатывает отзывы каждые 10 минут (или согласно POLL_INTERVAL).
Вы можете использовать /run для немедленной обработки.`
}

func (b *Bot) handleStatus() string {
	return `✅ *Статус сервиса*

🔄 *Автоматическая обработка:* Активна
📊 *База данных:* SQLite
📈 *Метрики:* Prometheus endpoint

Сервис работает в фоновом режиме и обрабатывает отзывы автоматически.`
}

func (b *Bot) handleRunNow(ctx context.Context) string {
	if b.service == nil {
		return "❌ Сервис не инициализирован"
	}

	go func() {
		b.log.Info("manual cycle triggered via telegram")
		b.service.HandleCycle(ctx)
	}()

	return `🚀 *Запуск обработки*

Цикл обработки отзывов запущен вручную. Результаты будут видны в логах.`
}
