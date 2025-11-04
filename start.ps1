# =============================================================================
# Скрипт запуска Feedback Bot для Windows PowerShell
# =============================================================================

Write-Host "Запуск Feedback Bot..." -ForegroundColor Green
Write-Host ""

# Установка токена Telegram бота
$env:TELEGRAM_TOKEN = "8303011908:AAEtfOAydoMDfEA_JzydZ29bQmPsQ9eZGM8"

# Опциональные настройки
$env:LOG_LEVEL = "debug"

# =============================================================================
# ВЫБОР БАЗЫ ДАННЫХ
# =============================================================================
# SQLite (по умолчанию) - для небольших проектов до 100-200 пользователей
$env:DB_TYPE = "sqlite"
$env:DB_PATH = "data/feedbacks.db"

# PostgreSQL - для масштабирования до 1000+ пользователей
# Раскомментируйте следующие строки для использования PostgreSQL:
# $env:DB_TYPE = "postgres"
# $env:DB_PATH = "host=localhost port=5432 user=postgres password=ваш_пароль dbname=feedbacks sslmode=disable"

# =============================================================================
# Обязательный канал для подписки
# =============================================================================
# ID канала (используется напрямую для надежной проверки подписки)
$env:REQUIRED_CHANNEL_ID = "-1003294078901"

# Username канала (для отображения в сообщениях)
$env:REQUIRED_CHANNEL = "novikovpromarket"

# =============================================================================
# Администратор бота (для команды /admin)
# =============================================================================
# Укажите ваш Telegram User ID для доступа к административной панели
# Чтобы узнать свой ID, напишите боту @userinfobot
$env:ADMIN_USER_ID = "7217012505"

Write-Host "==============================================================================" -ForegroundColor Cyan
Write-Host "КОНФИГУРАЦИЯ" -ForegroundColor Cyan
Write-Host "==============================================================================" -ForegroundColor Cyan
Write-Host ""

# Вывод информации о канале
Write-Host "[КАНАЛ]" -ForegroundColor Green
Write-Host "  Username: @$env:REQUIRED_CHANNEL" -ForegroundColor White
if ($env:REQUIRED_CHANNEL_ID) {
    Write-Host "  ID: $env:REQUIRED_CHANNEL_ID" -ForegroundColor White
}
Write-Host ""

# Вывод информации об администраторе
Write-Host "[АДМИНИСТРАТОР]" -ForegroundColor Green
if ($env:ADMIN_USER_ID) {
    Write-Host "  User ID: $env:ADMIN_USER_ID" -ForegroundColor White
    Write-Host "  Команда /admin доступна" -ForegroundColor White
} else {
    Write-Host "  НЕ НАСТРОЕН - команда /admin недоступна" -ForegroundColor Yellow
}
Write-Host ""

# Вывод информации о базе данных
Write-Host "[БАЗА ДАННЫХ]" -ForegroundColor Green
Write-Host "  Тип: $env:DB_TYPE" -ForegroundColor White
if ($env:DB_TYPE -eq "postgres") {
    Write-Host "  DSN: $($env:DB_PATH -replace 'password=[^ ]+', 'password=***')" -ForegroundColor White
} else {
    Write-Host "  Путь: $env:DB_PATH" -ForegroundColor White
}
Write-Host ""

# Проверка, что переменные канала установлены
if (-not $env:REQUIRED_CHANNEL_ID -and -not $env:REQUIRED_CHANNEL) {
    Write-Host "==============================================================================" -ForegroundColor Red
    Write-Host "ОШИБКА: Переменные для проверки подписки НЕ установлены!" -ForegroundColor Red
    Write-Host "==============================================================================" -ForegroundColor Red
    Write-Host ""
    Write-Host "Проверка подписки будет ОТКЛЮЧЕНА!" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "Для включения проверки подписки установите:" -ForegroundColor Yellow
    Write-Host "  `$env:REQUIRED_CHANNEL_ID = `"-1003294078901`"" -ForegroundColor Cyan
    Write-Host "  `$env:REQUIRED_CHANNEL = `"novikovpromarket`"" -ForegroundColor Cyan
    Write-Host ""
} else {
    Write-Host "[ПРОВЕРКА ПОДПИСКИ]" -ForegroundColor Green
    Write-Host "  Статус: ВКЛЮЧЕНА" -ForegroundColor White
    Write-Host ""
}

Write-Host "==============================================================================" -ForegroundColor Yellow
Write-Host "ВАЖНО!" -ForegroundColor Yellow
Write-Host "==============================================================================" -ForegroundColor Yellow
Write-Host "  1. Бот должен быть добавлен в канал @novikovpromarket как администратор!" -ForegroundColor White
Write-Host "     Это необходимо для проверки подписчиков." -ForegroundColor White
Write-Host ""
Write-Host "  2. Найдите вашего бота в Telegram и отправьте /start" -ForegroundColor White
Write-Host ""
Write-Host "  3. Для администратора: отправьте /admin для просмотра статистики" -ForegroundColor White
Write-Host "==============================================================================" -ForegroundColor Yellow
Write-Host ""

# Проверка переменных перед запуском
Write-Host "Проверка переменных окружения..." -ForegroundColor Cyan
Write-Host ""

$allOk = $true

if ($env:TELEGRAM_TOKEN) {
    Write-Host "  [OK] TELEGRAM_TOKEN установлен" -ForegroundColor Green
} else {
    Write-Host "  [!!] TELEGRAM_TOKEN НЕ установлен" -ForegroundColor Red
    $allOk = $false
}

if ($env:REQUIRED_CHANNEL_ID) {
    Write-Host "  [OK] REQUIRED_CHANNEL_ID = $env:REQUIRED_CHANNEL_ID" -ForegroundColor Green
} else {
    Write-Host "  [!!] REQUIRED_CHANNEL_ID НЕ установлен" -ForegroundColor Red
    $allOk = $false
}

if ($env:REQUIRED_CHANNEL) {
    Write-Host "  [OK] REQUIRED_CHANNEL = $env:REQUIRED_CHANNEL" -ForegroundColor Green
} else {
    Write-Host "  [!!] REQUIRED_CHANNEL НЕ установлен" -ForegroundColor Red
    $allOk = $false
}

if ($env:ADMIN_USER_ID) {
    Write-Host "  [OK] ADMIN_USER_ID = $env:ADMIN_USER_ID" -ForegroundColor Green
} else {
    Write-Host "  [--] ADMIN_USER_ID НЕ установлен (опционально)" -ForegroundColor Yellow
}

Write-Host ""

if (-not $allOk) {
    Write-Host "КРИТИЧЕСКАЯ ОШИБКА: Не все обязательные переменные установлены!" -ForegroundColor Red
    Write-Host "Нажмите любую клавишу для выхода..." -ForegroundColor Yellow
    $null = $Host.UI.RawUI.ReadKey("NoEcho,IncludeKeyDown")
    exit 1
}

Write-Host "==============================================================================" -ForegroundColor Green
Write-Host "ЗАПУСК БОТА..." -ForegroundColor Green
Write-Host "==============================================================================" -ForegroundColor Green
Write-Host ""

go run ./cmd/feedback-bot
