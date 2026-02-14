#!/bin/bash
# Restrict Docker Swarm host-published ports to localhost and Docker-internal networks.
# These ports are for internal services (NATS, Alexandria, agent gateways) and should
# not be reachable from the network. Docker bypasses UFW, so we use DOCKER-USER chain.
#
# Applied by: docker-user-firewall.service (systemd oneshot)

set -e

PORTS="4222,8222,8500,18790"

# Flush existing rules in DOCKER-USER (Docker recreates the chain on restart)
iptables -F DOCKER-USER 2>/dev/null || true
ip6tables -F DOCKER-USER 2>/dev/null || true

# IPv4: allow localhost + Docker internal, drop everything else for these ports
iptables -I DOCKER-USER -p tcp -m multiport --dports "$PORTS" -j DROP
iptables -I DOCKER-USER -p tcp -m multiport --dports "$PORTS" -s 172.16.0.0/12 -j RETURN
iptables -I DOCKER-USER -p tcp -m multiport --dports "$PORTS" -s 10.0.0.0/8 -j RETURN
iptables -I DOCKER-USER -p tcp -m multiport --dports "$PORTS" -s 127.0.0.0/8 -j RETURN

# IPv6: same pattern
ip6tables -I DOCKER-USER -p tcp -m multiport --dports "$PORTS" -j DROP
ip6tables -I DOCKER-USER -p tcp -m multiport --dports "$PORTS" -s ::1/128 -j RETURN
ip6tables -I DOCKER-USER -p tcp -m multiport --dports "$PORTS" -s fd00::/8 -j RETURN
ip6tables -I DOCKER-USER -p tcp -m multiport --dports "$PORTS" -s fe80::/10 -j RETURN

echo "docker-user-firewall: restricted ports $PORTS to localhost"
