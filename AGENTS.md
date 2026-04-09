# AGENTS.md — Инструкции для AI ассистентов

## 📌 Контекст проекта

Это VPN сервис на Go, состоящий из двух компонентов:
- **vpnservice** — сервер, поднимает TUN интерфейс, принимает WebSocket подключения
- **vpnclient** — клиент, подключается к серверу через WebSocket, поднимает L3 туннель

## 🏗 Архитектура

```
cmd/vpnservice/main.go      ← Сервер (точка входа)
cmd/vpnclient/main.go       ← Клиент (точка входа)

internal/config/            ← Конфигурация (YAML парсинг, настройки)
internal/protocol/          ← Протокол инкапсуляции (форматы сообщений)
internal/tun/               ← TUN интерфейс (создание, настройка, чтение/запись)
internal/ws/                ← WebSocket транспорт (сервер, клиент, прокси)
internal/routes/            ← Управление маршрутами (netlink, приоритеты)
```

## 🔑 Ключевые решения

1. **Go версия:** 1.19 (зависимости совместимы, не обновлять без проверки)
2. **WebSocket библиотека:** `gorilla/websocket` v1.5.3
3. **TUN интерфейс:** **syscall `ioctl TUNSETIFF`** (без внешних зависимостей, Linux)
4. **Маршруты:** `vishvananda/netlink` v1.1.0
5. **Прокси:** `golang.org/x/net/proxy` (HTTP + SOCKS5)
6. **Сборка:** всегда статическая — `CGO_ENABLED=0 go build`

## ✅ Что реализовано

### Протокол (internal/protocol/message.go)
- Формат сообщений: 4-байтовый заголовок (Type, Flags, PayloadLength)
- Типы: DATA (0x01), CONTROL (0x02), KEEPALIVE (0x03), FRAGMENT (0x04)
- Control подтипы: AUTH_*, ROUTES_*, STATISTICS, ERROR, DISCONNECT
- Сериализация/десериализация сообщений
- Фрагментация для пакетов >65KB
- ParseAuthResponsePayload — исправлена формула проверки длины
- ParseAuthSuccessPayload — парсинг назначенного IP

### Конфигурация (internal/config/config.go)
- YAML парсинг для сервера и клиента
- Настраиваемые таймауты (аутентификация, keepalive, фрагментация)
- TLS опционален (сервер: `tls.enabled`, клиент: `use_tls`)
- Пользователи сервера в конфиге (username/password)

### TUN интерфейс (internal/tun/tun.go)
- Создание TUN через syscall `ioctl TUNSETIFF` (без внешних зависимостей)
- Настройка IPv4/IPv6 адресов
- Функции AddRoute, AddRoute6, DeleteRoute (через exec ip)
- IsIPv6Packet() — определение версии пакета

### WebSocket транспорт (internal/ws/transport.go)
- Сервер: слушает порт, поддерживает ws:// и wss://
- Клиент: подключение с поддержкой прокси
- Прокси: HTTP (через http.Transport) и SOCKS5 (через proxy.SOCKS5)

### Маршруты (internal/routes/routes.go)
- Менеджер маршрутов (ApplyRoutes, ClearRoutes)
- Объединение серверных и клиентских маршрутов
- Клиентские маршруты имеют больший приоритет (серверным +1000 к метрике)
- Применение через netlink (Linux)

### Тестирование
- test-netns.sh — скрипт для тестирования через network namespaces
- Полная изоляция сервера и клиента в разных сетевых пространствах

## 🐛 Исправления

1. **ParseAuthResponsePayload** — неверная формула проверки длины payload
   - Было: `2+usernameLen+1+passwordLen`
   - Стало: `1+usernameLen+1+passwordLen`

2. **Concurrent write to WebSocket** — panic при одновременной записи
   - Добавлен `sync.Mutex` для всех записей в WebSocket
   - Сервер: tunToWS, keepaliveMonitor, wsToTUN
   - Клиент: tunToWS, keepalive, wsToTUN

3. **I/O timeout** — соединение разрывалось по таймауту
   - Добавлен read deadline на сервере и клиенте
   - Обновляется после получения каждого сообщения
   - Keepalive monitor на сервере

4. **IP not assigned** — клиент не получал IP
   - Клиент теперь парсит AUTH_SUCCESS payload
   - TUN пересоздаётся с назначенным IP (10.0.0.2)

## 🔄 Последовательность подключения

```
Client                          Server
  │                               │
  │──── WebSocket Handshake ────→│
  │←─── AUTH_CHALLENGE ──────────│
  │──── AUTH_RESPONSE ──────────→│ (username, password)
  │←─── AUTH_SUCCESS ────────────│ (ClientIP, ServerIP, Subnet)
  │←─── ROUTES_CONFIG ───────────│ (маршруты сервера)
  │⟷─── DATA / KEEPALIVE ───────│
  │──── DISCONNECT ─────────────→│
```

## 📝 Следующие задачи (из QWEN.md)

- [ ] Добавление сжатия (флаг COMPRESSED)
- [ ] Unit тесты (protocol, config, routes)
- [ ] Логирование и метрики (Prometheus)
- [ ] Systemd unit файлы
- [ ] Docker контейнеризация
- [ ] Документация по развёртыванию
- [ ] Интеграционные тесты

## 🧪 Тестирование

### Docker-тестирование (рекомендуемый способ)

Самый простой способ — запустить сервер в Docker контейнере, клиент на хосте (или наоборот):

```bash
# 1. Собрать статические бинарники
CGO_ENABLED=0 go build -o vpnservice ./cmd/vpnservice
CGO_ENABLED=0 go build -o vpnclient ./cmd/vpnclient

# 2. Запустить контейнер с пробросом порта
docker run --rm -it \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -v /home/iazarov/vpn:/vpn \
  -p 8443:8443 \
  ubuntu:22.04 bash

# 3. Внутри контейнера — сервер
/vpn/vpnservice -config /vpn/test-server.yaml

# 4. На хосте (или в другой консоли контейнера) — клиент
sudo /vpn/vpnclient -config /vpn/test-client.yaml

# 5. В третьей консоли — проверка
ping -I vpnclient0 10.0.0.1
```

**Почему работает:** сервер слушит `0.0.0.0:8443`, Docker пробрасывает порт, клиент подключается через `localhost:8443`.

### Автоматический тест через network namespaces

```bash
sudo ./test-netns.sh

# Очистка
sudo ./test-netns.sh clean
```

### Ручной тест (одна машина, разные терминалы)

```bash
sudo ./vpnservice -config test-server.yaml   # терминал 1
sudo ./vpnclient -config test-client.yaml    # терминал 2
ping 10.0.0.1                                # терминал 3
```

## ⚠️ Важные ограничения

1. **Root права** — TUN и маршруты требуют root/CAP_NET_ADMIN
2. **Только Linux** — другие ОС не тестировались/ограничены
3. **Go 1.19** — зависимости совместимы с этой версией
4. **TLS опционален** — можно использовать ws:// и wss://

## 🛠 Команды для работы

```bash
# Сборка (всегда статическая!)
CGO_ENABLED=0 go build -o vpnservice ./cmd/vpnservice
CGO_ENABLED=0 go build -o vpnclient ./cmd/vpnclient

# Сборка всех пакетов
go build ./...

# Проверка зависимостей
go mod tidy

# Запуск (требует root)
sudo ./vpnservice -config server.yaml
sudo ./vpnclient -config client.yaml
```

## 📚 Документация

- **QWEN.md** — общая информация о проекте
- **PROTOCOL.md** — полная спецификация протокола
- **RESEARCH.md** — исследование библиотек и аналогов

---

*Последнее обновление: 09 Апреля 2026*
*TUN переведён на syscall (без внешних зависимостей), бинарники статические*
