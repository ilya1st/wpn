# VPN Service — Туннельный сервис через WebSocket

## 📖 Описание

Проект представляет собой VPN сервис на базе Go, использующий WebSocket как транспорт для IP/IPv6 пакетов через TUN интерфейс.

### Компоненты

| Компонент | Путь | Описание |
|-----------|------|----------|
| **vpnservice** | `cmd/vpnservice/` | Сервер — поднимает TUN, принимает WebSocket подключения |
| **vpnclient** | `cmd/vpnclient/` | Клиент — подключается к серверу, поднимает L3 туннель |

## 🏗 Архитектура

```
┌─────────────────┐         WebSocket (HTTP/HTTPS)        ┌─────────────────┐
│   vpnclient     │ ─────────────────────────────────────→│   vpnservice    │
│                 │                                       │                 │
│  TUN интерфейс  │ ←─── IP/IPv6 пакеты (бинарные) ─────→ │  TUN интерфейс  │
│  + маршруты     │         с заголовками протокола       │  + маршруты     │
│  + прокси       │                                       │  + авторизация  │
└─────────────────┘                                       └─────────────────┘
```

## 📁 Структура проекта

```
vpn/
├── cmd/
│   ├── vpnservice/main.go     # Точка входа сервера
│   └── vpnclient/main.go      # Точка входа клиента
├── internal/
│   ├── config/config.go       # Конфигурация (YAML, сервер/клиент)
│   ├── protocol/message.go    # Протокол инкапсуляции (сериализация)
│   ├── tun/                   # TUN интерфейс (кроссплатформенный)
│   │   ├── tun.go             # Общий код (Interface, маршруты, утилиты)
│   │   ├── tun_linux.go       # Linux реализация (syscall TUNSETIFF)
│   │   └── tun_darwin.go      # macOS реализация (utun control-сокеты)
│   ├── ws/transport.go        # WebSocket транспорт (сервер/клиент/прокси)
│   └── routes/routes.go       # Управление маршрутами (netlink)
├── server.example.yaml        # Пример конфига сервера
├── client.example.yaml        # Пример конфига клиента
├── test-server.yaml           # Тестовый конфиг сервера
├── test-client.yaml           # Тестовый конфиг клиента
├── test-netns.sh              # Скрипт тестирования через netns
├── RESEARCH.md                # Исследование библиотек
├── PROTOCOL.md                # Полная спецификация протокола
├── go.mod                     # Go модуль
├── go.sum                     # Зависимости
├── QWEN.md                    # Этот файл
└── AGENTS.md                  # Инструкции для AI ассистентов
```

## 🔌 Используемые библиотеки

```go
github.com/gorilla/websocket v1.5.3       # WebSocket
github.com/vishvananda/netlink v1.1.0     # Управление маршрутами
golang.org/x/net v0.20.0                  // Прокси поддержка
gopkg.in/yaml.v3 v3.0.1                   # YAML конфигурация
```

**Версия Go:** 1.19 (минимальная)

**TUN интерфейс:** кроссплатформенная реализация (Linux: syscall `TUNSETIFF`, macOS: utun control-сокеты)

## ✅ Реализовано

- [x] TUN интерфейс для IPv4 и IPv6 (Linux + macOS)
- [x] WebSocket транспорт (HTTP/HTTPS, TLS опционально)
- [x] Аутентификация по логину/паролю из конфига
- [x] Настраиваемый таймаут аутентификации
- [x] Протокол с заголовками (DATA, CONTROL, KEEPALIVE, FRAGMENT)
- [x] Фрагментация пакетов >65KB
- [x] Поддержка HTTP и SOCKS5 прокси на клиенте
- [x] Управление маршрутами (сервер + клиент, приоритет клиентских)
- [x] Keepalive механизм (30с интервал, 90с таймаут) — сервер + клиент
- [x] YAML конфигурация с таймаутами
- [x] Базовый PoC (компилируется и работает)
- [x] Получение IP клиентом из AUTH_SUCCESS
- [x] Синхронизация записи в WebSocket (sync.Mutex)
- [x] Read deadline мониторинг соединения
- [x] Тестирование через network namespaces
- [x] Настраиваемый WebSocket path (server.path, client.ws_location)
- [x] TLS поддержка (wss://, self-signed сертификаты)
- [x] allow_insecure — пропуск проверки сертификата на клиенте

## 🐛 Исправления

- **ParseAuthResponsePayload** — исправлена формула проверки длины payload
- **Concurrent write to WebSocket** — добавлен mutex для всех записей
- **I/O timeout** — добавлен read deadline на сервере и клиенте
- **IP not assigned** — клиент теперь получает IP из AUTH_SUCCESS payload
- **macOS compilation** — TUN интерфейс разделён на платформо-зависимые файлы (build tags)
- **macOS utun** — исправлено создание utun сокета через RawSyscall (AF_SYSTEM не поддерживается syscall.Socket)
- **macOS sockaddr_ctl** — корректная структура: 32 байта, `[5]uint32` reserved, `ss_sysaddr = AF_SYS_CONTROL`
- **macOS interface name** — всегда используется реальное имя (utun0/1/...), имя из конфига игнорируется
- **macOS ifconfig inet** — добавлен destination адрес для point-to-point utun интерфейсов
- **macOS utun 4-byte header** — strip/add Protocol Family заголовка для совместимости с Linux TUN
- **macOS routes** — кроссплатформенные маршруты (Linux: netlink, macOS: route add)
- **macOS route add** — сервер автоматически добавляет маршрут на подсеть `10.0.0.0/24` через TUN
- **macOS interface name** — для маршрутов используется `tunIface.Name()` (`utunX`), а не имя из конфига

## 🚀 Быстрый старт

### Сборка

```bash
CGO_ENABLED=0 go build -o vpnservice ./cmd/vpnservice
CGO_ENABLED=0 go build -o vpnclient ./cmd/vpnclient
```

### Запуск сервера

```bash
# Скопировать пример конфига
cp server.example.yaml server.yaml
# Отредактировать server.yaml под свои нужды

# Запустить сервер (требует root для TUN)
sudo ./vpnservice -config server.yaml
```

### Запуск клиента

```bash
# Скопировать пример конфига
cp client.example.yaml client.yaml
# Отредактировать client.yaml (указать сервер, логин/пароль)

# Запустить клиент (требует root для TUN)
sudo ./vpnclient -config client.yaml
```

## 📋 Следующие шаги

- [ ] Добавление сжатия (флаг COMPRESSED)
- [ ] клиент при соединении по HTTPS должен маскироваться под реальные браузеры по TLS отпечаткам (fingerprinting) и поддерживать подмену TLS SNI
- [ ] Unit тесты (protocol, config, routes)
- [ ] Логирование и метрики (Prometheus)
- [ ] Systemd unit файлы
- [ ] Docker контейнеризация
- [ ] Документация по развёртыванию
- [ ] Интеграционные тесты

## 🧪 Тестирование

### Docker-тестирование (рекомендуемый способ)

Проще всего запустить сервер в Docker, клиент на хосте:

```bash
# Контейнер с сервером
docker run --rm -it \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -v /home/iazarov/vpn:/vpn \
  -p 8443:8443 \
  bash bash

# Внутри контейнера
/vpn/vpnservice -config /vpn/test-server.yaml

# На хосте (другая консоль)
sudo ./vpnclient -config test-client.yaml

# Проверка
ping -I vpnclient0 10.0.0.1
```

### Локальное тестирование (одна машина)

```bash
# Запуск через network namespaces (полная изоляция)
sudo ./test-netns.sh

# Очистка после тестов
sudo ./test-netns.sh clean
```

### Ручное тестирование

```bash
# Терминал 1 — сервер
sudo ./vpnservice -config test-server.yaml

# Терминал 2 — клиент
sudo ./vpnclient -config test-client.yaml

# Терминал 3 — проверка
ping 10.0.0.1
```

## ⚠️ Важные заметки

1. **Root права** — для работы с TUN интерфейсом и маршрутами требуются права root или `CAP_NET_ADMIN`
2. **Поддерживаемые ОС** — Linux (полная поддержка), macOS (TUN через utun, маршруты через ifconfig)
3. **Статическая сборка** — всегда использовать `CGO_ENABLED=0 go build`
4. **Кросс-компиляция** — можно собирать для macOS из Linux: `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build`
5. **TLS опционален** — можно использовать как `ws://` так и `wss://`
6. **Go 1.19** — зависимости совместимы с этой версией (не использовать новее без проверки)
7. **Маршруты** — клиентские маршруты имеют больший приоритет чем серверные (метрика +1000 к серверным)

## 🔗 Документация

- **PROTOCOL.md** — полная спецификация протокола (форматы сообщений, последовательности)
- **RESEARCH.md** — результаты исследования библиотек и аналогов
- **AGENTS.md** — инструкции для AI ассистентов (как продолжать работу)

---

*Последнее обновление: 10 Апреля 2026*
*Полная кроссплатформенность: TUN, маршруты, пинги между macOS и Linux работают*

## Qwen Added Memories
- Задача на будущее: клиент при соединении по HTTPS должен маскироваться под реальные браузеры по TLS отпечаткам (fingerprinting) и поддерживать подмену TLS SNI
- Всегда собирать Go бинарники статически: CGO_ENABLED=0 go build
