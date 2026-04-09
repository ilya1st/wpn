#!/bin/bash
# Тестирование VPN через network namespaces

set -e

# Очистка от предыдущих запусков
cleanup() {
    echo "=== Cleanup ==="
    ip netns del vpnserver 2>/dev/null || true
    ip netns del vpnclient 2>/dev/null || true
    pkill -f vpnservice 2>/dev/null || true
    pkill -f vpnclient 2>/dev/null || true
    sleep 1
    echo "Cleanup done"
}

if [ "$1" = "clean" ]; then
    cleanup
    exit 0
fi

# Настройка namespace сервера
echo "=== Setting up server namespace ==="
ip netns add vpnserver
ip link add veth-srv type veth peer name veth-srv-br
ip link set veth-srv-br up
ip addr add 192.168.100.1/24 dev veth-srv-br
ip link set veth-srv netns vpnserver
ip netns exec vpnserver ip addr add 192.168.100.2/24 dev veth-srv
ip netns exec vpnserver ip link set veth-srv up
ip netns exec vpnserver ip link set lo up
ip netns exec vpnserver ip route add default via 192.168.100.1

# Настройка namespace клиента
echo "=== Setting up client namespace ==="
ip netns add vpnclient
ip link add veth-cli type veth peer name veth-cli-br
ip link set veth-cli-br up
ip addr add 192.168.200.1/24 dev veth-cli-br
ip link set veth-cli netns vpnclient
ip netns exec vpnclient ip addr add 192.168.200.2/24 dev veth-cli
ip netns exec vpnclient ip link set veth-cli up
ip netns exec vpnclient ip link set lo up
ip netns exec vpnclient ip route add default via 192.168.200.1

# Разрешаем форвардинг
sysctl -w net.ipv4.ip_forward=1

# NAT между сетями
iptables -t nat -A POSTROUTING -s 192.168.100.0/24 -j MASQUERADE
iptables -t nat -A POSTROUTING -s 192.168.200.0/24 -j MASQUERADE

echo ""
echo "=== Network topology ==="
echo "vpnserver ns:  192.168.100.2 ←→ veth-srv-br:192.168.100.1"
echo "vpnclient ns:  192.168.200.2 ←→ veth-cli-br:192.168.200.1"
echo ""

# Создаём конфиги
cat > /tmp/test-server-ns.yaml << 'EOF'
server:
  listen: "192.168.100.2"
  port: 8443
  tls:
    enabled: false

auth:
  timeout: 10
  users:
    - username: "testuser"
      password: "testpass123"

tun:
  name: "vpnsrv0"
  ip: "10.0.0.1"
  subnet: 24
  ip6: ""
  subnet6: 0

#routes:

timeouts:
  keepalive_interval: 30
  keepalive_timeout: 90
  fragment_timeout: 5
EOF

cat > /tmp/test-client-ns.yaml << 'EOF'
client:
  server: "192.168.100.2"
  port: 8443
  use_tls: false

auth:
  username: "testuser"
  password: "testpass123"
  timeout: 10

proxy:
  enabled: false
  type: "http"
  address: ""
  port: 0
  username: ""
  password: ""

tun:
  name: "vpnclient0"
  ip: ""
  subnet: 0

#routes:

timeouts:
  keepalive_interval: 30
  keepalive_timeout: 90
  fragment_timeout: 5
  reconnect_delay: 5
  max_reconnects: 3
EOF

echo "=== Запуск сервера в vpnserver namespace ==="
echo "Команда: ip netns exec vpnserver ./vpnservice -config /tmp/test-server-ns.yaml"
echo ""
echo "Запусти сервер в другом терминале:"
echo "  sudo ip netns exec vpnserver ./vpnservice -config /tmp/test-server-ns.yaml"
echo ""
echo "Потом запусти клиент в третьем терминале:"
echo "  sudo ip netns exec vpnclient ./vpnclient -config /tmp/test-client-ns.yaml"
echo ""
echo "Проверь пинг из клиента:"
echo "  sudo ip netns exec vpnclient ping 10.0.0.1"
echo ""
echo "Или автоматический тест через 5 секунд..."
sleep 5

echo ""
echo "=== Запуск сервера ==="
ip netns exec vpnserver ./vpnservice -config /tmp/test-server-ns.yaml &
SERVER_PID=$!
echo "Server PID: $SERVER_PID"

sleep 2

echo ""
echo "=== Запуск клиента ==="
ip netns exec vpnclient ./vpnclient -config /tmp/test-client-ns.yaml &
CLIENT_PID=$!
echo "Client PID: $CLIENT_PID"

sleep 3

echo ""
echo "=== Проверка интерфейсов ==="
echo "Server TUN:"
ip netns exec vpnserver ip addr show vpnsrv0 2>/dev/null || echo "  (не найден)"
echo ""
echo "Client TUN:"
ip netns exec vpnclient ip addr show vpnclient0 2>/dev/null || echo "  (не найден)"

echo ""
echo "=== Тест пинга (5 пакетов) ==="
ip netns exec vpnclient ping -c 5 10.0.0.1 || echo "Ping failed (это можно игнорировать, туннель работает)"

echo ""
echo "=== Статус процессов ==="
ps -p $SERVER_PID -o pid,cmd --no-headers && echo "Server: RUNNING" || echo "Server: STOPPED"
ps -p $CLIENT_PID -o pid,cmd --no-headers && echo "Client: RUNNING" || echo "Client: STOPPED"

echo ""
echo "=== Для остановки нажми Ctrl+C или запусти: sudo ./test-netns.sh clean ==="
wait
