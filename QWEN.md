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
├── test-server-docker.yaml    # Тестовый конфиг сервера для Docker
├── test-client1-docker.yaml   # Тестовый конфиг клиента 1 для Docker
├── test-client2-docker.yaml   # Тестовый конфиг клиента 2 для Docker
├── test-docker.sh             # Скрипт тестирования через Docker Compose
├── docker-compose-test.yaml   # Docker Compose для тестирования
├── Dockerfile                 # Минимальный образ для контейнеров
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
- [x] Поддержка HTTP и SOCKS5 прокси на клиенте
- [x] Управление маршрутами (сервер + клиент, приоритет клиентских)
- [x] Keepalive механизм (30с интервал, 90с таймаут) — сервер + клиент
- [x] YAML конфигурация с таймаутами
- [x] Базовый PoC (компилируется и работает)
- [x] Получение IP клиентом из AUTH_SUCCESS
- [x] Синхронизация записи в WebSocket (sync.Mutex)
- [x] Read deadline мониторинг соединения
- [x] Тестирование через Docker Compose (сервер + N клиентов, изолированные контейнеры)
- [x] Настраиваемый WebSocket path (server.path, client.ws_location)
- [x] TLS поддержка (wss://, self-signed сертификаты)
- [x] allow_insecure — пропуск проверки сертификата на клиенте
- [x] Сжатие пакетов (zlib, по пакетам, FlagCompressed)
- [x] Переименовано: `timeouts` → `connection_settings`

## ⚠️ Требует проверки

- [ ] **Фрагментация пакетов >65KB** — протокол определён (`CreateFragmentMessage`, `ParseFragmentHeader` в `protocol/message.go`), конфиг `fragment_timeout` парсится, но нет кода разбиения пакетов на фрагменты при записи и сборки при чтении. Нужно проверить: реально ли пакеты >65KB передаются через TUN/WebSocket, и доделать механизм фрагментации + assembler

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

## 🔴 Известные проблемы

### Архитектурные

1. **Один общий TUN для всех клиентов** — сервер использует один TUN-интерфейс, пакеты от всех клиентов смешиваются. При broadcast-записи каждый клиент получает трафик всех остальных. Нет per-client isolation. Требуется либо один TUN на клиента, либо IP-based маршрутизация внутри сервера.

### Функциональные

2. **Нет реконнекта после установленной сессии** — конфиг имеет `max_reconnects` и `reconnect_delay`, но реконнект работает только до аутентификации. Если соединение рвётся после AUTH_SUCCESS — клиент выходит, TUN не восстанавливается.
3. **Нет graceful shutdown сервера** — `http.ListenAndServe` блокирует, нет `http.Server.Shutdown`, нет механизма корректного завершения активных сессий.
4. **Фрагментация не реализована** — см. секцию «Требует проверки» выше.
5. **Нет статистики** — протокол определяет STATISTICS (0x16), но сбор и отправка отсутствуют.
6. **Нет ROUTES_UPDATE** — клиент не может отправить серверу свои маршруты.

### Качество кода

7. **Игнорируемые ошибки записи в WebSocket** — `conn.WriteMessage(...)` без проверки ошибки при AUTH_FAILURE, keepalive, disconnect.
8. **Дублирование кода сервер/клиент** — `tunToWS`, `wsToTUN`, `keepalive` практически идентичны. Можно вынести в общий пакет.
9. **Нет валидации IP-пакетов** — пакеты из TUN не проверяются на корректность перед отправкой. **Примечание:** возможно TCP-стек macOS/Linux сам это делает — нужно исследовать, не делаем ли мы двойную работу.
10. **Пароли логируются в открытом виде** — оставлено намеренно для этапа отладки, но перед релизом убрать.

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

### Docker Compose тестирование (сервер + 2 клиента)

```bash
# Собирает бинари, поднимает 3 контейнера с изолированными network namespace
./test-docker.sh up

# Автоматический тест с пингами и tcpdump
./test-docker.sh test

# Остановка
./test-docker.sh down
```

## 📋 Следующие шаги

### P0 — критические (порядок выполнения)

- [ ] **Сущность «сессия клиента»** — UUID, реестр сессий на сервере, session ID в протоколе, клиент хранит и передаёт session ID
- [ ] **Per-session isolation** — пакеты маршрутизируются по session ID, не broadcast всем
- [ ] **Keepalive мониторинг** — сервер детектит «мёртвые» сессии по таймауту, закрывает и чистит
- [ ] **Reconnect с учётом сессии** — сервер принимает re-auth, создаёт новую сессию, клиент восстанавливает TUN и маршруты
- [ ] **Graceful shutdown сервера** — `http.Server.Shutdown`, корректное завершение активных сессий, очистка реестра

### P1 — функциональные

- [ ] TLS fingerprinting + SNI подмена
- [ ] Статистика (STATISTICS)
- [ ] ROUTES_UPDATE от клиента
- [ ] Валидация IP-пакетов (требует исследования — возможно TCP-стек ОС уже делает)

### P2 — качество кода

- [ ] Дублирование кода сервер/клиент (`tunToWS`, `wsToTUN`, `keepalive`)
- [ ] Игнорируемые ошибки записи в WebSocket (частично исправлено)
- [ ] Unit тесты (protocol, config, routes)
- [ ] Логирование и метрики (Prometheus)

### P3 — инфраструктура

- [ ] Внешняя БД пользователей
- [ ] Лимит сессий на пользователя
- [ ] Systemd unit файлы
- [ ] Docker контейнеризация (production)
- [ ] Документация по развёртыванию
- [ ] Интеграционные тесты

## 🧪 Тестирование

### Docker Compose (рекомендуемый способ — сервер + N клиентов)

Изолированные контейнеры с собственными network namespace, tcpdump, ping:

```bash
# Запуск (собирает бинари и поднимает сервер + 2 клиента)
./test-docker.sh up

# Автоматический тест (up + пинги + tcpdump)
./test-docker.sh test

# Проверки вручную (интерактивный режим)
docker exec -it vpn-server     tcpdump -i vpnsrv0 -n
docker exec -it vpn-client-1   tcpdump -i vpnclient0 -n
docker exec -it vpn-client-1   ping -c 5 10.0.0.1
docker exec -it vpn-client-2   ping -c 5 10.0.0.1

# Остановка
./test-docker.sh down
```

### Docker-тестирование (вручную, один контейнер)

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

### Ручное тестирование (одна машина, разные терминалы)

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

*Последнее обновление: 16 Апреля 2026*
*Полная кроссплатформенность: TUN, маршруты, пинги между macOS и Linux работают*

## Qwen Added Memories
- Задача на будущее: клиент при соединении по HTTPS должен маскироваться под реальные браузеры по TLS отпечаткам (fingerprinting) и поддерживать подмену TLS SNI
- Всегда собирать Go бинарники статически: CGO_ENABLED=0 go build
