function buildVLESSURI(name: string, uuid: string, domain: string): string {
  return `vless://${uuid}@${domain}:443?encryption=none&security=tls&type=ws&host=${domain}&path=%2Fvless&sni=${domain}#${name}-VLESS`;
}

function buildTrojanURI(name: string, password: string, domain: string): string {
  return `trojan://${password}@${domain}:443?security=tls&type=ws&host=${domain}&path=%2Ftrojan&sni=${domain}#${name}-Trojan`;
}

export function buildSubscriptionForClient(
  username: string,
  rows: Array<{ vless_uuid: string; trojan_pw: string; vpn_host: string; node_id: string }>
): { subscription_url: string } {
  const lines: string[] = [];
  for (const row of rows) {
    lines.push(buildVLESSURI(`${username}-${row.node_id}`, row.vless_uuid, row.vpn_host));
    lines.push(buildTrojanURI(`${username}-${row.node_id}`, row.trojan_pw, row.vpn_host));
  }
  return { subscription_url: lines.join("\n") };
}
