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
internal/session/           ← Реестр сессий, пулы адресов, маршрутизация по IP
internal/fragment/          ← Фрагментация больших пакетов
internal/compression/       ← Сжатие пакетов (zlib)
```

## 🔑 Ключевые решения

1. **Go версия:** 1.19 (зависимости совместимы, не обновлять без проверки)
2. **WebSocket библиотека:** `gorilla/websocket` v1.5.3
3. **TUN интерфейс:** **кроссплатформенный** (Linux: syscall `TUNSETIFF`, macOS: utun control-сокеты, build tags)
4. **Маршруты:** `vishvananda/netlink` v1.1.0 (Linux), `ifconfig` (macOS)
5. **Прокси:** `golang.org/x/net/proxy` (HTTP + SOCKS5)
6. **Сборка:** всегда статическая — `CGO_ENABLED=0 go build`

## ✅ Что реализовано

### Протокол (internal/protocol/message.go)
- Формат сообщений: 4-байтовый заголовок (Type, Flags, PayloadLength)
- Типы: DATA (0x01), CONTROL (0x02), KEEPALIVE (0x03), FRAGMENT (0x04)
- Control подтипы: AUTH_*, ROUTES_*, STATISTICS, ERROR, DISCONNECT
- Сериализация/десериализация сообщений
- **Фрагментация**: полностью реализована — `Fragmenter` (клиент) разбивает пакеты >65KB, `Assembler` (сервер+клиент) собирает обратно с таймаутом и cleanup. Интегрирована в `cmd/vpnservice/main.go` и `cmd/vpnclient/main.go`
- ParseAuthResponsePayload — исправлена формула проверки длины
- ParseAuthSuccessPayload — парсинг SessionID + IPv4 + IPv6 (dual-stack)
- **AUTH_SUCCESS v2**: SessionID(1+len) + ClientIP4_len(1) + ClientIP4 + ClientIP6_len(1) + ClientIP6 + ServerIP4_len(1) + ServerIP4 + ServerIP6_len(1) + ServerIP6 + Subnet4 + Subnet6

### Конфигурация (internal/config/config.go)
- YAML парсинг для сервера и клиента
- Настраиваемые таймауты (аутентификация, keepalive, фрагментация)
- TLS опционален (сервер: `tls.enabled`, клиент: `use_tls`)
- Пользователи сервера в конфиге (username/password/**ip4**/**ip6**)
- WebSocket path: `server.path` (сервер), `client.ws_location` (клиент)
- TLS: `server.tls.enabled` + `cert/key`, `client.use_tls`, `client.allow_insecure`
- Тестовые SSL конфиги: `test-ssl-server.yaml` / `test-ssl-client.yaml` с самоподписанным сертификатом (SAN: localhost, 127.0.0.1)
- **Сжатие**: `connection_settings.compression` (zlib, по пакетам, FlagCompressed)
- **Конфиг**: `timeouts` → `connection_settings` (ServerConnectionSettings, ClientConnectionSettings)
- **UserEntry.ip4/ip6** — статические адреса для клиентов
- **Сервер**: `send_packet_buffer_size` (размер канала записи, по умолчанию 256), `write_channel_timeout` (таймаут записи, по умолчанию 5с), `reconnect_timeout` (хранение сессии для реконнекта, по умолчанию 300с)

### TUN интерфейс (internal/tun/)
- **tun.go** — общий код: Interface, Config, маршруты, утилиты
- **tun_linux.go** — Linux реализация (`//go:build linux`)
  - Создание через `/dev/net/tun` + `ioctl TUNSETIFF`
  - Настройка через `ip` команды
- **tun_darwin.go** — macOS реализация (`//go:build darwin`)
  - Создание через `/dev/utun` + control-сокеты (`CTLIOCGINFO`, `connect`)
  - Настройка через `ifconfig` команды
- Общий интерфейс `fileInterface` для абстракции файловых операций
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

### Сессии (internal/session/session.go)
- **Session** — UUID, Login, IsDynamicIP4/6, IP4, IP6, ClientIP, WSConn, State, LastActivity, DTStart, DTReconnect, ReconnectCount, Bytes/Packets Sent/Recv, ClientVersion
- **SessionState** — `SessionActive`, `SessionReconnecting`, `SessionExpired`
- **Registry** — реестр сессий с hashmap `byIP4`/`byIP6` для O(1) поиска сессии по IP
- **IPPool** — пул динамических адресов из подсети TUN
- `Session.InitWriter(bufferSize)` — инициализация буферизованного канала записи
- `Session.StartWriter(writeTimeout, onError)` — запуск writer'а; **без keepalive** (только DATA); `writeTimeout` применяется к записи в канал И в WebSocket
- `Session.QueueWrite(data, timeout)` — **блокирующий с таймаутом** (не неблокирующий); при истечении — `false`
- `Session.writeToConn(data, timeout)` — ставит `SetWriteDeadline` перед `WriteMessage`
- `Registry.ReconnectingSessions()` — возвращает сессии в состоянии реконнекта
- `Registry.RemoveSession()` — **НЕ освобождает IP** если сессия в `SessionReconnecting`
- `sessionCleanup()` — активные без активности → `SessionReconnecting`; реконнект дольше `reconnect_timeout` → удаление; keepalive от клиента обновляет `LastActivity`

### Тестирование

- test-docker.sh — скрипт для тестирования через Docker Compose (рекомендуемый)
  - Поднимает сервер + 2 клиента в изолированных контейнерах
  - Каждый контейнер имеет свой network namespace
  - Поддержка tcpdump, ping через `docker exec -it`

## 🐛 Исправления

1. **ParseAuthResponsePayload** — неверная формула проверки длины payload
   - Было: `2+usernameLen+1+passwordLen`
   - Стало: `1+usernameLen+1+passwordLen`

2. **Concurrent write to WebSocket** — panic при одновременной записи
   - Сервер: `Session.WritePacket()` с внутренней блокировкой `mu`
   - Клиент: `wsMutex` для `tunToWS` + `keepalive`

3. **I/O timeout** — соединение разрывалось по таймауту
   - Добавлен read deadline на сервере и клиенте
   - Обновляется после получения любого сообщения
   - Keepalive monitor на сервере

4. **IP not assigned** — клиент не получал IP
   - Клиент теперь парсит AUTH_SUCCESS payload
   - TUN пересоздаётся с назначенным IP

5. **macOS compilation** — `syscall.TUNSETIFF` не доступен на macOS
   - TUN разделён на платформо-зависимые файлы с build tags
   - Linux: `tun_linux.go` (TUNSETIFF), macOS: `tun_darwin.go` (utun)
   - Кросс-компиляция: `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build`

6. **macOS utun исправления** — серия фиксов для корректной работы на macOS:
   - Сокет через `RawSyscall(SYS_SOCKET, AF_SYSTEM=32, SOCK_DGRAM, SYSPROTO_CONTROL)`
   - `sockaddr_ctl`: 32 байта, `[5]uint32` reserved, `ss_sysaddr = AF_SYS_CONTROL`
   - Имя интерфейса: всегда реальное (`utun0/1/...`), имя из конфига игнорируется
   - `ifconfig inet`: point-to-point формат `<local> <dest> netmask <mask>`

7. **macOS маршруты** — кроссплатформенная реализация:
   - `routes_common.go` — общий код (Manager, ApplyRoutes)
   - `routes_linux.go` — netlink (Linux)
   - `routes_darwin.go` — `route add/delete` через exec (macOS)
   - Сервер автоматически добавляет маршрут на подсеть `10.0.0.0/24`
   - Используется `tunIface.Name()` (`utunX`) а не `cfg.TUN.Name`

8. **macOS пинги** — работают между сервером macOS и клиентом Linux

9. **WebSocket path** — настраиваемый путь через `server.path` и `client.ws_location`

10. **Broadcast всем клиентам** — **РЕШЕНО**: per-session маршрутизация через `Registry.GetSessionByIP()` (O(1) hashmap)

11. **Пароли логируются** — **РЕШЕНО**: убрано логирование паролей в открытом виде

12. **Неблокирующий дроп пакетов** — **РЕШЕНО**: `QueueWrite` и `writeToConn` стали блокирующими с таймаутом; при переполнении канала сессия переводится в `SessionReconnecting`, IP сохраняются для реконнекта; сервер больше не шлёт keepalive (только читает от клиента)

## 🔄 Последовательность подключения

```
Client                          Server
  │                               │
  │──── WebSocket Handshake ────→│
  │←─── AUTH_CHALLENGE ──────────│
  │──── AUTH_RESPONSE ──────────→│ (username, password)
  │←─── AUTH_SUCCESS ────────────│ (SessionID, ClientIP4/6, ServerIP4/6, Subnet4/6)
  │←─── ROUTES_CONFIG ───────────│ (маршруты сервера)
  │⟷─── DATA / KEEPALIVE ───────│
  │──── DISCONNECT ─────────────→│
```

## 🔁 Реконнект и Camouflage

Подробная концепция и план из 3 шагов — в **RECONNECT-POLITICS-CONCEPTS.md**:

- **Шаг 1:** Базовый реконнект (восстановление сессии по SessionID, без буфера)
- **Шаг 2:** Camouflage режим (CONTROL 0x19 CAMOUFLAGE_DISCONNECT, рандомные дисконнекты от сервера, принудительный сброс)
- **Шаг 3:** Reconnect Buffer (буферизация при SessionReconnecting, drain при восстановлении, 0 потерь пакетов)

Сервер работает в двух режимах: с маскировкой и без (конфиг `camouflage.enabled`).

## 📝 Следующие задачи (из QWEN.md)

### P0 — критические (порядок выполнения)

- [ ] **Reconnect с учётом сессии** — сервер принимает re-auth по session ID, восстанавливает сессию
- [ ] **Graceful shutdown сервера** — `http.Server.Shutdown`, корректное завершение сессий

### P1 — функциональные

- [ ] **Поддержка IPv6 и тестирование** — поднять IPv6 туннель в Docker, проверить ping
- [ ] TLS fingerprinting + SNI подмена
- [ ] Статистика (STATISTICS)
- [ ] ROUTES_UPDATE от клиента

### P2 — качество кода

- [ ] Дублирование кода сервер/клиент
- [ ] **Транспортный интерфейс** — абстрактный `Transport` (`Connect/Read/Write/Close`), обёртка `WebSocketTransport`, замена `*websocket.Conn` на интерфейс
- [ ] Unit тесты (protocol, config, routes, session)

### P3 — инфраструктура

- [ ] Systemd, Docker production, документация

## 🧪 Тестирование

### Docker Compose (рекомендуемый способ)

```bash
# 1. Собрать и поднять сервер + 2 клиента
./test-docker.sh up

# 2. Автоматический тест с пингами и tcpdump
./test-docker.sh test

# 3. Остановка
./test-docker.sh down

# Ручные проверки (интерактивный режим, обязательно -it):
docker exec -it vpn-server     tcpdump -i vpnsrv0 -n
docker exec -it vpn-client-1   tcpdump -i vpnclient0 -n
docker exec -it vpn-client-1   ping -c 5 10.0.0.1
docker exec -it vpn-client-2   ping -c 5 10.0.0.1
```

**Почему работает:** все контейнеры в одной Docker-сети `vpn-net` (172.30.0.0/16), сервер слушит `0.0.0.0:8443`, клиенты подключаются к `172.30.0.10`.

### Ручной тест (одна машина, разные терминалы)

```bash
sudo ./vpnservice -config test-server.yaml   # терминал 1
sudo ./vpnclient -config test-client.yaml    # терминал 2
ping 10.0.0.1                                # терминал 3
```

## ⚠️ Важные ограничения

1. **Root права** — TUN и маршруты требуют root/CAP_NET_ADMIN
2. **Поддерживаемые ОС** — Linux (полная поддержка), macOS (TUN через utun, маршруты через ifconfig)
3. **Go 1.19** — зависимости совместимы с этой версией
4. **TLS опционален** — можно использовать ws:// и wss://
5. **Кросс-компиляция** — работает из коробки: `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build`

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

*Последнее обновление: 25 Апреля 2026*
*Реестр сессий, per-session маршрутизация, пулы IP, dual-stack AUTH_SUCCESS, фрагментация пакетов, блокирующий QueueWrite с таймаутом, SessionReconnecting, сохранение IP при реконнекте*
