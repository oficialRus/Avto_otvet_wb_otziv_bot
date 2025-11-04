# Feedback Bot Startup Script (Simplified)

$env:TELEGRAM_TOKEN = "8303011908:AAEtfOAydoMDfEA_JzydZ29bQmPsQ9eZGM8"
$env:LOG_LEVEL = "debug"
$env:DB_TYPE = "sqlite"
$env:DB_PATH = "data/feedbacks.db"
$env:REQUIRED_CHANNEL_ID = "-1003294078901"
$env:REQUIRED_CHANNEL = "novikovpromarket"
$env:ADMIN_USER_ID = "7217012505"

Write-Host "===============================================================================" -ForegroundColor Cyan
Write-Host "FEEDBACK BOT - CONFIGURATION" -ForegroundColor Cyan
Write-Host "===============================================================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "Channel ID: $env:REQUIRED_CHANNEL_ID" -ForegroundColor Green
Write-Host "Channel: @$env:REQUIRED_CHANNEL" -ForegroundColor Green
Write-Host "Admin ID: $env:ADMIN_USER_ID" -ForegroundColor Green
Write-Host "Database: $env:DB_TYPE" -ForegroundColor Green
Write-Host ""
Write-Host "===============================================================================" -ForegroundColor Yellow
Write-Host "IMPORTANT: Bot must be admin in channel @novikovpromarket" -ForegroundColor Yellow
Write-Host "===============================================================================" -ForegroundColor Yellow
Write-Host ""
Write-Host "Starting bot..." -ForegroundColor Green
Write-Host ""

go run ./cmd/feedback-bot

