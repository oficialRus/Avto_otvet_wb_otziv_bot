# Скрипт запуска Feedback Bot для Windows PowerShell

Write-Host "🚀 Запуск Feedback Bot..." -ForegroundColor Green

# Установка токена Telegram бота
$env:TELEGRAM_TOKEN = "8303011908:AAEtfOAydoMDfEA_JzydZ29bQmPsQ9eZGM8"

# Опциональные настройки
$env:LOG_LEVEL = "info"

# Запуск приложения
Write-Host "✅ Токен установлен" -ForegroundColor Yellow
Write-Host "📱 Ищите вашего бота в Telegram и отправьте /start" -ForegroundColor Cyan
Write-Host ""
go run ./cmd/feedback-bot

