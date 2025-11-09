@echo off
chcp 65001 >nul
echo ===============================================================================
echo Запуск Feedback Bot
echo ===============================================================================
echo.

REM Установка переменных окружения
set TELEGRAM_TOKEN=8447547104:AAF6IZSfMsYhz33yI9AtWNXogrdKmNDzYtk
set LOG_LEVEL=info
set DB_TYPE=sqlite
set DB_PATH=data/feedbacks.db
set REQUIRED_CHANNEL_ID=-1003294078901
set REQUIRED_CHANNEL=novikovpromarket
set ADMIN_USER_ID=7217012505
set METRICS_ADDR=:8080

echo [КОНФИГУРАЦИЯ]
echo   Telegram Token: SET
echo   Database Type: %DB_TYPE%
echo   Database Path: %DB_PATH%
echo   Channel ID: %REQUIRED_CHANNEL_ID%
echo   Channel: @%REQUIRED_CHANNEL%
echo   Admin User ID: %ADMIN_USER_ID%
echo   Log Level: %LOG_LEVEL%
echo.

REM Создание директории для базы данных
if not exist "data" mkdir data

echo ===============================================================================
echo ВАЖНО!
echo ===============================================================================
echo   1. Бот должен быть добавлен в канал @novikovpromarket как администратор!
echo   2. Найдите вашего бота в Telegram и отправьте /start
echo   3. Для администратора: отправьте /admin для просмотра статистики
echo ===============================================================================
echo.

REM Запуск программы
if exist "feedback-bot.exe" (
    echo Запуск собранного бинарника...
    echo.
    feedback-bot.exe
) else if exist "feedback-bot" (
    echo Запуск бинарника...
    echo.
    feedback-bot
) else (
    echo Запуск через go run...
    echo.
    go run ./cmd/feedback-bot
)

pause

