# Production Deployment Checklist

## Перед деплоем на сервер

### 1. Переключитесь на PostgreSQL
```env
DB_TYPE=postgres
DATABASE_URL=postgres://user:password@host:5432/dbname
```

### 2. Настройте переменные окружения
```env
TELEGRAM_TOKEN=ваш_токен
REQUIRED_CHANNEL_ID=-1003294078901
REQUIRED_CHANNEL=novikovpromarket
ADMIN_USER_ID=7217012505
LOG_LEVEL=info  # не debug!
DB_TYPE=postgres
DATABASE_URL=postgres://...
```

### 3. Используйте systemd service
Создайте файл `/etc/systemd/system/feedback-bot.service`:
```ini
[Unit]
Description=Feedback Bot
After=network.target postgresql.service

[Service]
Type=simple
User=feedback-bot
WorkingDirectory=/opt/feedback-bot
ExecStart=/opt/feedback-bot/feedback-bot
Restart=always
RestartSec=5

# Environment
EnvironmentFile=/opt/feedback-bot/.env

[Install]
WantedBy=multi-user.target
```

### 4. Настройте мониторинг
- Prometheus на порту :8080/metrics
- Grafana dashboards
- Alerting для критических ошибок

### 5. Настройте бэкапы
```bash
# Для PostgreSQL
pg_dump -U user dbname > backup.sql

# Cron job
0 3 * * * pg_dump -U user dbname | gzip > /backups/db-$(date +\%Y\%m\%d).sql.gz
```

### 6. Настройте логирование
- Ротация логов (logrotate)
- Централизованный сбор (ELK stack)

### 7. Безопасность
- Firewall правила (только нужные порты)
- SSL/TLS для PostgreSQL
- Ограничение доступа к метрикам

### 8. Масштабирование
При необходимости можно запустить несколько инстансов бота:
- Каждый инстанс работает независимо
- PostgreSQL обеспечивает консистентность
- Используйте load balancer при необходимости

## Команды для деплоя

```bash
# 1. Создайте пользователя
sudo useradd -m -s /bin/bash feedback-bot

# 2. Скопируйте файлы
sudo mkdir /opt/feedback-bot
sudo cp feedback-bot /opt/feedback-bot/
sudo cp .env /opt/feedback-bot/
sudo chown -R feedback-bot:feedback-bot /opt/feedback-bot

# 3. Запустите сервис
sudo systemctl daemon-reload
sudo systemctl enable feedback-bot
sudo systemctl start feedback-bot

# 4. Проверьте статус
sudo systemctl status feedback-bot
sudo journalctl -u feedback-bot -f
```

## Мониторинг здоровья

Проверьте:
1. Метрики: http://server:8080/metrics
2. Логи: journalctl -u feedback-bot
3. База данных: количество записей, размер
4. Память/CPU: htop, vmstat

## Лимиты для production

С PostgreSQL и хорошим сервером (4 CPU, 8GB RAM):
- 5,000+ активных пользователей
- 50,000+ зарегистрированных
- 100+ отзывов/минуту

При превышении - горизонтальное масштабирование.
