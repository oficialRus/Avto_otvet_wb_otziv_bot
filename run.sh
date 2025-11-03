#!/bin/bash

# Скрипт запуска сервиса автоответов на отзывы Wildberries с Telegram ботом

# Обязательные переменные окружения
export WB_TOKEN="${WB_TOKEN:-your_wb_token_here}"
export TELEGRAM_TOKEN="8303011908:AAEtfOAydoMDfEA_JzydZ29bQmPsQ9eZGM8"

# Опциональные переменные (имеют значения по умолчанию)
# export LOG_LEVEL="info"              # debug, info, warn, error
# export POLL_INTERVAL="10m"          # интервал опроса отзывов
# export DB_PATH="data/feedbacks.db"  # путь к базе данных
# export METRICS_ADDR=":8080"         # адрес для метрик Prometheus

# Запуск приложения
./feedback-bot

# Или для разработки:
# go run ./cmd/feedback-bot

