#!/bin/bash
# Wire Mesh 상태 확인 스크립트

echo "=== Wire Mesh 상태 ==="
echo ""
printf "%-8s %-18s %-12s %s\n" "노드" "VPN IP" "상태" "역할"
printf "%-8s %-18s %-12s %s\n" "----" "------" "----" "----"

COORDINATOR="http://158.247.247.115:8790"

# VPS (relay nodes)
for h in "v1:158.247.247.115" "v2:158.247.199.164" "v3:141.164.37.98" "v4:66.135.27.166"; do
  name=$(echo $h | cut -d: -f1)
  ip=$(echo $h | cut -d: -f2)
  st=$(ssh -o ConnectTimeout=3 -o BatchMode=yes root@$ip "systemctl is-active wire" 2>/dev/null || echo "?")
  vpn=$(curl -s $COORDINATOR/peers | grep -o "\"node_name\":\"$name\"[^}]*" | grep -o "10\.98\.[0-9]*\.[0-9]*")
  printf "%-8s %-18s %-12s %s\n" "$name" "$vpn" "$st" "relay"
done

# NAT clients
for name in m1 n1 d1 d2 s1 s2 g1 g2 g3 g4; do
  vpn=$(curl -s $COORDINATOR/peers | grep -o "\"node_name\":\"$name\"[^}]*" | grep -o "10\.98\.[0-9]*\.[0-9]*")
  if [ -n "$vpn" ]; then
    ping -c 1 -W 1 $vpn > /dev/null 2>&1 && st="reachable" || st="unreachable"
    printf "%-8s %-18s %-12s %s\n" "$name" "$vpn" "$st" "client"
  fi
done

echo ""
echo "총 노드: $(curl -s $COORDINATOR/peers | grep -o '"node_name"' | wc -l | tr -d ' ')"
