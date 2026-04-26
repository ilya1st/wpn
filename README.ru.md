# VPN over WebSocket

[🇬🇧 English version](README.md)

PoC VPN-сервиса на Go, использующего стандартные протоколы связи (HTTP/HTTPS + WebSocket) как транспорт для IP/IPv6 пакетов через TUN-интерфейс.

## 🎯 Что это

Клиент поднимает TUN-интерфейс на своей машине, инкапсулирует IP-пакеты в WebSocket-соединение и отправляет серверу. Сервер распаковывает пакеты и передаёт их в свой TUN-интерфейс — и наоборот. Трафик выглядит как обычное WebSocket-соединение на порту 443.

```
┌─────────────┐         WebSocket (ws:// или wss://)        ┌─────────────┐
│  vpnclient  │ ──────────────────────────────────────────→│ vpnservice  │
│             │                                             │             │
│  TUN iface  │ ←─── IP/IPv6 пакеты в binary-сообщениях ──→│  TUN iface  │
│  + routes   │         через HTTP/HTTPS транспорт          │  + routes   │
│  + proxy    │                                             │  + auth     │
└─────────────┘                                             └─────────────┘
```

## ⚠️ Статус проекта

**Это PoC (Proof of Concept).** Код компилируется и работает, но не предназначен для production без доработки.

### Реализовано

- [x] TUN-интерфейс для IPv4 и IPv6 (Linux + macOS)
- [x] WebSocket транспорт через HTTP/HTTPS (TLS опционально)
- [x] Аутентификация по логину/паролю из конфига
- [x] Протокол с заголовками (DATA, CONTROL, KEEPALIVE, FRAGMENT)
- [x] Поддержка HTTP и SOCKS5 прокси на клиенте
- [x] Управление маршрутами (сервер + клиент, приоритет клиентских)
- [x] Keepalive-механизм (30с интервал, 90с таймаут)
- [x] Сжатие пакетов (zlib, по пакетам)
- [x] Фрагментация больших пакетов (>65 КБ)
- [x] Реестр сессий с пулами IPv4/IPv6 адресов
- [x] Per-session маршрутизация (пакеты направляются конкретной сессии по IP, не broadcast)
- [x] Статические IP для клиентов (конфиг `auth.users`)
- [x] Session cleanup и состояние `SessionReconnecting`
- [x] Блокирующая запись с таймаутом (без молчаливого дропа пакетов)
- [x] Тестирование через Docker Compose (сервер + N клиентов)

### В планах

- [ ] **Реконнект с восстановлением сессии** — клиент восстанавливает соединение после обрыва без потери IP
- [ ] **Camouflage-режим** — сервер периодически принудительно дисконнектит клиента для имитации поведения браузера (защита от DPI)
- [ ] **Буфер при реконнекте** — буферизация пакетов на время разрыва, отправка при восстановлении (0 потерь)
- [ ] **TLS fingerprinting + SNI-подмена** — маскировка отпечатка клиента под реальный браузер
- [ ] Graceful shutdown сервера
- [ ] Статистика (STATISTICS), ROUTES_UPDATE от клиента
- [ ] Полноценная поддержка IPv6 в тестировании

Полный план — в [RECONNECT-POLITICS-CONCEPTS.md](RECONNECT-POLITICS-CONCEPTS.md).

## 🚀 Быстрый старт

### Сборка

```bash
CGO_ENABLED=0 go build -o vpnservice ./cmd/vpnservice
CGO_ENABLED=0 go build -o vpnclient ./cmd/vpnclient
```

Всегда статическая сборка (`CGO_ENABLED=0`) — это требование для кросс-компиляции и деплоя без зависимостей.

### Запуск сервера

1. Скопируй пример конфига и отредактируй:

```bash
cp server.example.yaml server.yaml
```

2. Минимальный `server.yaml`:

```yaml
server:
  listen: "0.0.0.0"
  port: 8443
  path: "/ws"

auth:
  users:
    - username: "user1"
      password: "password1111"

tun:
  name: "vpnsrv0"
  ip: "10.0.0.1"
  subnet: 24
```

3. Запусти (требует `root` / `CAP_NET_ADMIN` для TUN):

```bash
sudo ./vpnservice -config server.yaml
```

### Запуск клиента

1. Скопируй пример конфига и отредактируй:

```bash
cp client.example.yaml client.yaml
```

2. Минимальный `client.yaml`:

```yaml
client:
  server: "10.0.0.1"    # или домен сервера
  port: 8443
  ws_location: "/ws"

auth:
  username: "user1"
  password: "password1111"

tun:
  name: "vpnclient0"
  ip: ""  # авто — получит от сервера
```

3. Запусти (требует `root` / `CAP_NET_ADMIN`):

```bash
sudo ./vpnclient -config client.yaml
```

### Проверка

После подключения клиент получит IP из пула сервера. Проверь пингом:

```bash
ping 10.0.0.1        # до сервера
ping -I vpnclient0 10.0.0.1  # через конкретный интерфейс
```

## 🔧 Конфигурация

### Сервер (`server.example.yaml`)

| Раздел | Что настраивает |
|--------|----------------|
| `server` | Listen-адрес, порт, WebSocket-путь, TLS (cert/key) |
| `auth` | Таймаут аутентификации, список пользователей (username/password/ip4/ip6) |
| `tun` | Имя TUN-интерфейса, IPv4/IPv6 адреса и подсети |
| `connection_settings` | Keepalive, фрагментация, сжатие, буфер записи, таймаут реконнекта |

### Клиент (`client.example.yaml`)

| Раздел | Что настраивает |
|--------|----------------|
| `client` | Адрес сервера, порт, TLS, WebSocket-путь |
| `auth` | Логин/пароль, таймаут аутентификации |
| `proxy` | HTTP или SOCKS5 прокси для подключения к серверу |
| `tun` | Имя TUN-интерфейса, IP (пусто = авто) |
| `connection_settings` | Keepalive, фрагментация, сжатие, задержка и макс. попытки реконнекта |

## 🐳 Docker-тестирование

Рекомендуемый способ — изолированные контейнеры с собственными network namespace:

```bash
# Поднять сервер + 2 клиента
./test-docker.sh up

# Автоматический тест с пингами и tcpdump
./test-docker.sh test

# Ручные проверки
docker exec -it vpn-server     tcpdump -i vpnsrv0 -n
docker exec -it vpn-client-1   ping -c 5 10.0.0.1
docker exec -it vpn-client-2   ping -c 5 10.0.0.1

# Остановить
./test-docker.sh down
```

## 📋 Поддерживаемые платформы

| ОС | TUN | Маршруты | Примечание |
|----|-----|----------|------------|
| Linux | `syscall.TUNSETIFF` | netlink | Полная поддержка |
| macOS | utun control-сокеты | `ifconfig` / `route` | Полная поддержка |

## 🔐 Безопасность и обход блокировок

### Базовый уровень (сейчас)

- TLS опционален (`wss://` через self-signed или внешние сертификаты)
- Аутентификация до начала передачи данных
- Трафик выглядит как обычное WebSocket-соединение на порту 443
- Работает за reverse proxy (nginx, haproxy)

### Для обхода DPI (в планах)

- TLS fingerprinting — маска под реальный браузер (Chrome/Firefox)
- SNI-подмена
- Camouflage-режим — сервер периодически дисконнектит клиента для имитации поведения браузера
- Буферизация при реконнекте — 0 потерь пакетов

Для продакшена рекомендуется ставить сервер за reverse proxy (nginx/haproxy) с нормальным доменом и TLS-сертификатом.

## 📂 Структура проекта

```
cmd/
  vpnservice/main.go      # Сервер (точка входа)
  vpnclient/main.go       # Клиент (точка входа)
internal/
  config/config.go        # YAML-конфигурация
  protocol/message.go     # Протокол инкапсуляции (сериализация)
  tun/                    # TUN-интерфейс (кроссплатформенный)
  ws/transport.go         # WebSocket транспорт + прокси
  routes/routes.go        # Управление маршрутами
  session/session.go      # Реестр сессий, пулы IP
  fragment/fragment.go    # Фрагментация больших пакетов
  compression/comp.go     # Сжатие (zlib)
```

Полная спецификация протокола — в [PROTOCOL.md](PROTOCOL.md).

## 🏗 Зависимости

| Библиотека | Для чего |
|-----------|----------|
| `gorilla/websocket` v1.5.3 | WebSocket |
| `vishvananda/netlink` v1.1.0 | Маршруты (Linux) |
| `golang.org/x/net` v0.20.0 | Прокси (HTTP + SOCKS5) |
| `gopkg.in/yaml.v3` v3.0.1 | YAML-конфигурация |
| `google/uuid` v1.6.0 | UUID сессий |

Минимальная версия **Go: 1.19**.

## 📝 Лицензия

[LICENSE](LICENSE)
