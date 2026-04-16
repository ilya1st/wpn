#!/bin/bash
# test-docker.sh — запуск тестирования VPN через Docker Compose
#
# Поднимает сервер и 2 клиента в изолированных Docker-контейнерах.
# Каждый контейнер имеет свой network namespace.
# Для проверок используйте docker exec -it (интерактивный режим):
#   docker exec -it vpn-server tcpdump -i vpnsrv0 -n
#   docker exec -it vpn-client-1 tcpdump -i vpnclient0 -n
#   docker exec -it vpn-client-1 ping -c 5 10.0.0.1

set -e

COMPOSE_FILE="docker-compose-test.yaml"
PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

cd "$PROJECT_DIR"

# Определяем доступный docker compose CLI
if docker compose version >/dev/null 2>&1; then
    DC="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
    DC="docker-compose"
else
    echo "ОШИБКА: docker compose plugin или docker-compose не найден"
    echo "Установите: docker compose plugin (v2+) или pip install docker-compose"
    exit 1
fi

echo "Используем: $DC"

usage() {
    echo "Использование: $0 {up|down|status|test|clean|help}"
    echo ""
    echo "  up    — собрать бинари, поднять контейнеры"
    echo "  down  — остановить контейнеры"
    echo "  clean — down + удалить образы и volumes"
    echo "  status — показать статус контейнеров"
    echo "  test  — up + автоматическая проверка пингов"
    echo "  help  — эта справка"
}

build() {
    echo "=== Сборка бинариев ==="
    CGO_ENABLED=0 go build -o vpnservice ./cmd/vpnservice
    CGO_ENABLED=0 go build -o vpnclient ./cmd/vpnclient
    echo "Готово: vpnservice, vpnclient"
}

dc() {
    $DC -f "$COMPOSE_FILE" "$@"
}

do_up() {
    build
    echo ""
    echo "=== Поднятие контейнеров ==="
    dc up -d --build

    echo ""
    echo "=== Ожидание запуска (10 сек) ==="
    sleep 10

    echo ""
    echo "=== Статус контейнеров ==="
    dc ps

    echo ""
    echo "=== Готово! Контейнеры работают. ==="
    echo ""
    echo "Полезные команды (интерактивный режим, docker exec -it):"
    echo "  docker exec -it vpn-server   tcpdump -i vpnsrv0 -n"
    echo "  docker exec -it vpn-client-1 tcpdump -i vpnclient0 -n"
    echo "  docker exec -it vpn-client-2 tcpdump -i vpnclient0 -n"
    echo "  docker exec -it vpn-client-1 ping -c 5 10.0.0.1"
    echo "  docker exec -it vpn-client-2 ping -c 5 10.0.0.1"
    echo "  docker exec -it vpn-client-1 ping -c 5 172.30.0.21  # клиент 2"
    echo "  docker exec -it vpn-client-2 ping -c 5 172.30.0.20  # клиент 1"
    echo ""
    echo "Для остановки: $0 down"
}

do_down() {
    echo "=== Остановка контейнеров ==="
    dc down
}

do_clean() {
    echo "=== Полная очистка ==="
    dc down --rmi local --volumes --remove-orphans
}

do_status() {
    dc ps
}

do_test() {
    build
    echo ""
    echo "=== Поднятие контейнеров ==="
    dc up -d --build

    echo ""
    echo "=== Ожидание запуска сервера (5 сек) ==="
    sleep 5

    echo ""
    echo "=== Ожидание подключения клиентов (10 сек) ==="
    sleep 10

    echo ""
    echo "=== Логи сервера ==="
    dc logs vpn-server --tail=20

    echo ""
    echo "=== Логи клиента 1 ==="
    dc logs vpn-client-1 --tail=20

    echo ""
    echo "=== Логи клиента 2 ==="
    dc logs vpn-client-2 --tail=20

    echo ""
    echo "=== Проверка: ping сервера от клиента 1 ==="
    docker exec vpn-client-1 ping -c 5 10.0.0.1 || echo "PING FAILED (возможно туннель ещё не поднялся)"

    echo ""
    echo "=== Проверка: ping сервера от клиента 2 ==="
    docker exec vpn-client-2 ping -c 5 10.0.0.1 || echo "PING FAILED (возможно туннель ещё не поднялся)"

    echo ""
    echo "=== Проверка: ping клиента 2 от клиента 1 ==="
    docker exec vpn-client-1 ping -c 3 172.30.0.21 || echo "PING FAILED (клиент 2 не доступен напрямую)"

    echo ""
    echo "=== Проверка: tcpdump на сервере (5 сек) ==="
    timeout 5 docker exec vpn-server tcpdump -i vpnsrv0 -n -c 10 || true

    echo ""
    echo "=== Тестирование завершено ==="
    echo "Для остановки: $0 down"
    echo "Для интерактивных проверок (обязательно с -it):"
    echo "  docker exec -it vpn-client-1 /bin/sh"
}

case "${1:-help}" in
    up)     do_up ;;
    down)   do_down ;;
    clean)  do_clean ;;
    status) do_status ;;
    test)   do_test ;;
    help)   usage ;;
    *)      echo "Неизвестная команда: $1"; usage; exit 1 ;;
esac
