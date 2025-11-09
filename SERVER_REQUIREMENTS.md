# 📋 Список требований для установки бота на сервер

## 🔧 Обязательные компоненты

### 1. Операционная система
- **Linux** (рекомендуется):
  - Ubuntu 20.04+ / 22.04+
  - Debian 11+ / 12+
  - CentOS 8+ / Rocky Linux 8+
  - Amazon Linux 2+
- **Windows Server** (также поддерживается, но не рекомендуется для production)

### 2. Go (для сборки бинарника)
- **Версия:** Go 1.24.3 или новее
- **Установка на Ubuntu/Debian:**
  ```bash
  # Добавить репозиторий Go
  wget https://go.dev/dl/go1.24.3.linux-amd64.tar.gz
  sudo tar -C /usr/local -xzf go1.24.3.linux-amd64.tar.gz
  
  # Добавить в PATH
  echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
  source ~/.bashrc
  
  # Проверить установку
  go version
  ```

- **Альтернатива:** Использовать готовый бинарник (не требует установки Go)

### 3. База данных (выберите один вариант)

#### Вариант A: PostgreSQL (рекомендуется для production)
- **Версия:** PostgreSQL 12+ (рекомендуется 14+)
- **Установка на Ubuntu/Debian:**
  ```bash
  sudo apt update
  sudo apt install postgresql postgresql-contrib
  
  # Проверить версию
  psql --version
  ```

- **Создание базы данных:**
  ```bash
  sudo -u postgres psql
  
  # В psql:
  CREATE DATABASE feedbacks;
  CREATE USER feedback_bot WITH PASSWORD 'ваш_надежный_пароль';
  GRANT ALL PRIVILEGES ON DATABASE feedbacks TO feedback_bot;
  \q
  ```

#### Вариант B: SQLite (для небольших проектов до 200 пользователей)
- **Включено в Go проект** - не требует отдельной установки
- **Требования:** Права на создание/запись файлов в директории проекта
- **Автоматически создается** при первом запуске

### 4. Системные зависимости (для компиляции)
- **На Ubuntu/Debian:**
  ```bash
  sudo apt update
  sudo apt install build-essential git
  ```

- **На CentOS/Rocky Linux:**
  ```bash
  sudo yum groupinstall "Development Tools"
  sudo yum install git
  ```

### 5. Сетевые порты
- **Порт 8080** (для метрик Prometheus) - должен быть доступен
- **Исходящие подключения:**
  - Telegram API: `api.telegram.org:443` (HTTPS)
  - Wildberries API: `feedbacks-api.wildberries.ru:443` (HTTPS)

### 6. Права доступа к файловой системе
- **Для SQLite:** Права на создание/запись в директории `data/`
- **Для PostgreSQL:** Доступ к базе данных с указанными credentials

---

## 🚀 Дополнительные компоненты (опционально, но рекомендуются)

### 7. Systemd (для автозапуска на Linux)
- **Обычно предустановлен** на всех современных Linux дистрибутивах
- **Проверка:**
  ```bash
  systemctl --version
  ```

### 8. Firewall (для безопасности)
- **UFW (Ubuntu/Debian):**
  ```bash
  sudo apt install ufw
  sudo ufw allow 22/tcp    # SSH
  sudo ufw allow 8080/tcp  # Метрики (опционально)
  sudo ufw enable
  ```

- **firewalld (CentOS/Rocky):**
  ```bash
  sudo firewall-cmd --permanent --add-port=8080/tcp
  sudo firewall-cmd --reload
  ```

### 9. Мониторинг (опционально)
- **Prometheus** - для сбора метрик
- **Grafana** - для визуализации метрик
- **Nginx** - для проксирования метрик (опционально)

### 10. Логирование (опционально)
- **logrotate** - для ротации логов
- **journald** - встроен в systemd (используется по умолчанию)

---

## 📦 Переменные окружения

### Обязательные:
```bash
TELEGRAM_TOKEN="ваш_токен_бота_от_BotFather"
```

### Рекомендуемые:
```bash
# База данных
DB_TYPE="postgres"  # или "sqlite" для небольших проектов
DB_PATH="host=localhost port=5432 user=feedback_bot password=*** dbname=feedbacks sslmode=disable"

# Канал (если требуется проверка подписки)
REQUIRED_CHANNEL_ID="-1003294078901"
REQUIRED_CHANNEL="novikovpromarket"

# Администратор
ADMIN_USER_ID="7217012505"

# Логирование
LOG_LEVEL="info"  # debug, info, warn, error

# Метрики (опционально)
METRICS_ADDR=":8080"
```

---

## 🔍 Проверка готовности сервера

### Минимальные требования:
- ✅ **RAM:** 512 MB (рекомендуется 2+ GB для production)
- ✅ **Диск:** 1 GB свободного места (рекомендуется 10+ GB)
- ✅ **CPU:** 1 ядро (рекомендуется 2+ ядра)

### Проверка перед установкой:
```bash
# 1. Проверить версию Go
go version

# 2. Проверить PostgreSQL (если используется)
psql --version
sudo systemctl status postgresql

# 3. Проверить доступность портов
netstat -tuln | grep 8080
ss -tuln | grep 8080

# 4. Проверить доступ к интернету
curl -I https://api.telegram.org
curl -I https://feedbacks-api.wildberries.ru

# 5. Проверить права доступа
mkdir -p data
touch data/test.txt
rm data/test.txt
```

---

## 📝 Быстрая установка (Ubuntu/Debian)

### Шаг 1: Установить Go
```bash
wget https://go.dev/dl/go1.24.3.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.24.3.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
go version
```

### Шаг 2: Установить PostgreSQL
```bash
sudo apt update
sudo apt install postgresql postgresql-contrib
sudo systemctl start postgresql
sudo systemctl enable postgresql

# Создать базу данных
sudo -u postgres psql
CREATE DATABASE feedbacks;
CREATE USER feedback_bot WITH PASSWORD 'ваш_пароль';
GRANT ALL PRIVILEGES ON DATABASE feedbacks TO feedback_bot;
\q
```

### Шаг 3: Скопировать проект на сервер
```bash
# Через git
git clone <repository-url> /opt/feedback-bot
cd /opt/feedback-bot

# Или через scp (с локального компьютера)
scp -r feedback-bot user@server:/opt/
```

### Шаг 4: Собрать бинарник
```bash
cd /opt/feedback-bot
go mod download
go build -o feedback-bot ./cmd/feedback-bot
```

### Шаг 5: Настроить переменные окружения
```bash
# Создать файл .env
cat > /opt/feedback-bot/.env << EOF
TELEGRAM_TOKEN=ваш_токен
DB_TYPE=postgres
DB_PATH=host=localhost port=5432 user=feedback_bot password=ваш_пароль dbname=feedbacks sslmode=disable
REQUIRED_CHANNEL_ID=-1003294078901
REQUIRED_CHANNEL=novikovpromarket
ADMIN_USER_ID=7217012505
LOG_LEVEL=info
METRICS_ADDR=:8080
EOF

# Установить права
chmod 600 /opt/feedback-bot/.env
```

### Шаг 6: Создать systemd service
```bash
sudo cat > /etc/systemd/system/feedback-bot.service << EOF
[Unit]
Description=Feedback Bot для Wildberries
After=network.target postgresql.service

[Service]
Type=simple
User=feedback-bot
WorkingDirectory=/opt/feedback-bot
ExecStart=/opt/feedback-bot/feedback-bot
Restart=always
RestartSec=10
EnvironmentFile=/opt/feedback-bot/.env

[Install]
WantedBy=multi-user.target
EOF

# Создать пользователя
sudo useradd -r -s /bin/false feedback-bot
sudo chown -R feedback-bot:feedback-bot /opt/feedback-bot

# Запустить сервис
sudo systemctl daemon-reload
sudo systemctl enable feedback-bot
sudo systemctl start feedback-bot
sudo systemctl status feedback-bot
```

---

## 🎯 Итоговый чек-лист

Перед запуском убедитесь, что:

- [ ] Go 1.24.3+ установлен
- [ ] PostgreSQL установлен и настроен (или SQLite доступен)
- [ ] База данных создана и пользователь имеет права доступа
- [ ] Проект скопирован на сервер
- [ ] Бинарник собран (`go build`)
- [ ] Переменные окружения настроены
- [ ] Systemd service создан (для автозапуска)
- [ ] Порт 8080 доступен (для метрик)
- [ ] Исходящие подключения к Telegram и WB API работают
- [ ] Firewall настроен (опционально)
- [ ] Бот запущен и работает (`systemctl status feedback-bot`)

---

## ❓ Часто задаваемые вопросы

### Можно ли использовать готовый бинарник вместо установки Go?
**Да!** Если у вас есть уже собранный бинарник (`feedback-bot` или `feedback-bot.exe`), то Go на сервере не нужен. Просто скопируйте бинарник и запустите его.

### Нужен ли PostgreSQL для небольшого количества пользователей?
**Нет.** Для до 200 пользователей можно использовать SQLite. Для большего количества пользователей рекомендуется PostgreSQL.

### Можно ли запустить без systemd?
**Да.** Можно запустить напрямую через `./feedback-bot` или использовать screen/tmux для фонового запуска. Но systemd рекомендуется для production.

### Нужен ли Nginx для работы бота?
**Нет.** Nginx нужен только если вы хотите проксировать метрики через домен. Бот работает самостоятельно.

---

## 📚 Дополнительная документация

- `DEPLOYMENT_GUIDE.md` - подробная инструкция по развертыванию
- `PRODUCTION_CHECKLIST.md` - чек-лист для production
- `README.md` - общая документация проекта

---

**Готово!** После выполнения всех шагов бот будет работать на сервере. 🚀

