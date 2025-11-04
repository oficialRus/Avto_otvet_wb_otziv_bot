@echo off
chcp 65001 >nul
echo ===============================================================================
echo Starting Feedback Bot...
echo ===============================================================================
echo.

REM Telegram Bot Token
set TELEGRAM_TOKEN=8303011908:AAEtfOAydoMDfEA_JzydZ29bQmPsQ9eZGM8

REM Log Level
set LOG_LEVEL=debug

REM Database Configuration
set DB_TYPE=sqlite
set DB_PATH=data/feedbacks.db

REM Channel Configuration
set REQUIRED_CHANNEL_ID=-1003294078901
set REQUIRED_CHANNEL=novikovpromarket

REM Admin User ID
set ADMIN_USER_ID=7217012505

echo [CONFIGURATION]
echo   Telegram Token: SET
echo   Channel ID: %REQUIRED_CHANNEL_ID%
echo   Channel Username: @%REQUIRED_CHANNEL%
echo   Admin User ID: %ADMIN_USER_ID%
echo   Database: %DB_TYPE% (%DB_PATH%)
echo.
echo ===============================================================================
echo IMPORTANT:
echo   1. Bot must be admin in channel @novikovpromarket
echo   2. Send /start to your bot in Telegram
echo   3. Admin can use /admin command
echo ===============================================================================
echo.
echo Starting bot...
echo.

go run ./cmd/feedback-bot

pause
