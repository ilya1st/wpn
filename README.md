# VPN over WebSocket

[🇷🇺 Русская версия](README.ru.md)

A PoC VPN service in Go that uses standard communication protocols (HTTP/HTTPS + WebSocket) as a transport for IP/IPv6 packets over a TUN interface.

## 🎯 What Is This

The client creates a TUN interface on its machine, encapsulates IP packets into a WebSocket connection, and sends them to the server. The server decapsulates packets and forwards them to its own TUN interface — and vice versa. Traffic looks like a regular WebSocket connection on port 443.

```
┌─────────────┐         WebSocket (ws:// or wss://)         ┌─────────────┐
│  vpnclient  │ ──────────────────────────────────────────→│ vpnservice  │
│             │                                             │             │
│  TUN iface  │ ←─── IP/IPv6 packets in binary messages ──→│  TUN iface  │
│  + routes   │         over HTTP/HTTPS transport           │  + routes   │
│  + proxy    │                                             │  + auth     │
└─────────────┘                                             └─────────────┘
```

## ⚠️ Project Status

**This is a PoC (Proof of Concept).** The code compiles and runs but is not ready for production without further development.

### Implemented

- [x] TUN interface for IPv4 and IPv6 (Linux + macOS)
- [x] WebSocket transport over HTTP/HTTPS (TLS optional)
- [x] Authentication by username/password from config
- [x] Protocol with headers (DATA, CONTROL, KEEPALIVE, FRAGMENT)
- [x] HTTP and SOCKS5 proxy support on the client
- [x] Route management (server + client, client routes have higher priority)
- [x] Keepalive mechanism (30s interval, 90s timeout)
- [x] Packet compression (zlib, per-packet)
- [x] Large packet fragmentation (>64 KB)
- [x] Session registry with IPv4/IPv6 address pools
- [x] Per-session routing (packets routed to specific sessions by IP, not broadcast)
- [x] Static IPs for clients (`auth.users` config)
- [x] Session cleanup and `SessionReconnecting` state
- [x] Blocking write with timeout (no silent packet drops)
- [x] Docker Compose testing (server + N clients)

### Planned

- [ ] **Reconnect with session restoration** — client recovers connection after drop without losing IP
- [ ] **Camouflage mode** — server periodically force-disconnects client to imitate browser behavior (DPI evasion)
- [ ] **Reconnect buffer** — packet buffering during connection drop, flush on recovery (0 losses)
- [ ] **TLS fingerprinting + SNI spoofing** — client fingerprint masquerading as a real browser
- [ ] Graceful server shutdown
- [ ] Statistics (STATISTICS), ROUTES_UPDATE from client
- [ ] Full IPv6 support in testing

Full roadmap — see [RECONNECT-POLITICS-CONCEPTS.md](RECONNECT-POLITICS-CONCEPTS.md).

## 🚀 Quick Start

### Build

```bash
CGO_ENABLED=0 go build -o vpnservice ./cmd/vpnservice
CGO_ENABLED=0 go build -o vpnclient ./cmd/vpnclient
```

Always build statically (`CGO_ENABLED=0`) — required for cross-compilation and dependency-free deployment.

### Running the Server

1. Copy the example config and edit it:

```bash
cp server.example.yaml server.yaml
```

2. Minimal `server.yaml`:

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

3. Start (requires `root` / `CAP_NET_ADMIN` for TUN):

```bash
sudo ./vpnservice -config server.yaml
```

### Running the Client

1. Copy the example config and edit it:

```bash
cp client.example.yaml client.yaml
```

2. Minimal `client.yaml`:

```yaml
client:
  server: "10.0.0.1"    # or server domain
  port: 8443
  ws_location: "/ws"

auth:
  username: "user1"
  password: "password1111"

tun:
  name: "vpnclient0"
  ip: ""  # auto — gets assigned by server
```

3. Start (requires `root` / `CAP_NET_ADMIN`):

```bash
sudo ./vpnclient -config client.yaml
```

### Verify

After connecting, the client receives an IP from the server pool. Test with ping:

```bash
ping 10.0.0.1                    # to server
ping -I vpnclient0 10.0.0.1     # via specific interface
```

## 🔧 Configuration

### Server (`server.example.yaml`)

| Section | Configures |
|---------|-----------|
| `server` | Listen address, port, WebSocket path, TLS (cert/key) |
| `auth` | Authentication timeout, user list (username/password/ip4/ip6) |
| `tun` | TUN interface name, IPv4/IPv6 addresses and subnets |
| `connection_settings` | Keepalive, fragmentation, compression, write buffer, reconnect timeout |

### Client (`client.example.yaml`)

| Section | Configures |
|---------|-----------|
| `client` | Server address, port, TLS, WebSocket path |
| `auth` | Username/password, authentication timeout |
| `proxy` | HTTP or SOCKS5 proxy for connecting to the server |
| `tun` | TUN interface name, IP (empty = auto) |
| `connection_settings` | Keepalive, fragmentation, compression, reconnect delay and max attempts |

## 🐳 Docker Testing

Recommended approach — isolated containers with their own network namespaces:

```bash
# Start server + 2 clients
./test-docker.sh up

# Automatic test with pings and tcpdump
./test-docker.sh test

# Manual checks
docker exec -it vpn-server     tcpdump -i vpnsrv0 -n
docker exec -it vpn-client-1   ping -c 5 10.0.0.1
docker exec -it vpn-client-2   ping -c 5 10.0.0.1

# Stop
./test-docker.sh down
```

## 📋 Supported Platforms

| OS | TUN | Routes | Notes |
|----|-----|--------|-------|
| Linux | `syscall.TUNSETIFF` | netlink | Full support |
| macOS | utun control sockets | `ifconfig` / `route` | Full support |

## 🔐 Security and Censorship Evasion

### Baseline (current)

- TLS optional (`wss://` via self-signed or external certificates)
- Authentication before data transfer
- Traffic looks like a regular WebSocket connection on port 443
- Works behind a reverse proxy (nginx, haproxy)

### For DPI evasion (planned)

- TLS fingerprinting — masquerade as a real browser (Chrome/Firefox)
- SNI spoofing
- Camouflage mode — server periodically disconnects client to imitate browser behavior
- Reconnect buffering — 0 packet losses during reconnection

For production, it is recommended to place the server behind a reverse proxy (nginx/haproxy) with a legitimate domain and TLS certificate.

## 📂 Project Structure

```
cmd/
  vpnservice/main.go      # Server (entry point)
  vpnclient/main.go       # Client (entry point)
internal/
  config/config.go        # YAML configuration
  protocol/message.go     # Encapsulation protocol (serialization)
  tun/                    # TUN interface (cross-platform)
  ws/transport.go         # WebSocket transport + proxy
  routes/routes.go        # Route management
  session/session.go      # Session registry, IP pools
  fragment/fragment.go    # Large packet fragmentation
  compression/comp.go     # Compression (zlib)
```

Full protocol specification — see [PROTOCOL.md](PROTOCOL.md).

## 🏗 Dependencies

| Library | Purpose |
|---------|---------|
| `gorilla/websocket` v1.5.3 | WebSocket |
| `vishvananda/netlink` v1.1.0 | Routes (Linux) |
| `golang.org/x/net` v0.20.0 | Proxy (HTTP + SOCKS5) |
| `gopkg.in/yaml.v3` v3.0.1 | YAML configuration |
| `google/uuid` v1.6.0 | Session UUIDs |

Minimum **Go version: 1.19**.

## 📝 License

[LICENSE](LICENSE)
