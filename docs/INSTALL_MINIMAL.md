# Cài nhanh cf-vpn trên VPS mới (tối giản)

## 1) Chuẩn bị VPS
- Ubuntu/Debian mới
- đăng nhập bằng user có sudo

## 2) Copy source sang VPS
```bash
git clone https://github.com/kulinh/cf-vpn.git
cd cf-vpn
```

## 3) Chạy bootstrap 1 lệnh
```bash
sudo -E CF_API_TOKEN='your_token' \
  CF_ACCOUNT_ID='your_account_id' \
  DOMAIN='vpn.example.com' \
  USER1_NAME='user1' \
  bash scripts/bootstrap-vps.sh
```

`USER1_NAME` có thể bỏ qua, mặc định là `user1`.

## 4) Kiểm tra sau cài
```bash
sudo cfvpnctl status
sudo cfvpnctl healthcheck run
sudo cfvpnctl gen-sub user1
```

Nếu lệnh `status` báo cả `cfvpn-xray` và `cfvpn-cloudflared` là active thì xong.
