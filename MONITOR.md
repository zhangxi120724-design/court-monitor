# Kallang Tennis Hub 场地监听脚本

这个脚本会定期检测 Kallang Tennis Hub 在特定时段的场地可用性，并在有空场地时通过 WhatsApp 发送实时通知。

## 功能特性

- ⏰ 定期检测场地可用性（默认每 5 分钟检查一次）
- 📅 智能时间窗口：工作日 8-10pm 或周末 5-10pm
- 🎾 支持 Indoor（1-7 号）和 Outdoor（8-15 号）场地
- 💬 通过 WhatsApp 实时通知
- ⏱️ 避免重复通知（同一场地 1 小时内只通知一次）

## 前置条件

### 1. PerfectGym 认证

首先确保已经初始化过认证：

```bash
# 首次运行（可视化模式）
HEADLESS=false PERFECTGYM_EMAIL=your-email PERFECTGYM_PASSWORD=your-password go run .

# 这会在 .auth/storage-state.json 中保存登录态
```

### 2. WhatsApp Business Account

- 申请 [WhatsApp Business Account](https://www.whatsapp.com/business/)
- 设置业务账户并获取 API Token
- 获取 Phone ID（您的 WhatsApp 业务号码）

> 如果你没有 WhatsApp Business Account，可以用 WhatsApp Cloud API 的测试模式。

### 3. 获取 API Token

在 [Meta Developers Console](https://developers.facebook.com/docs/whatsapp/cloud-api) 中：
1. 创建或选择你的 App
2. 添加 WhatsApp Product
3. 在 API Setup 中生成 Permanent Access Token
4. 记下 Phone Number ID

## 配置

在 `.env` 文件中添加以下变量：

```bash
# 现有的 PerfectGym 配置
PERFECTGYM_BASE_URL=https://thekallang.perfectgym.com/clientportal2/...
PERFECTGYM_EMAIL=your-email@example.com
PERFECTGYM_PASSWORD=your-password

# WhatsApp 通知配置
WHATSAPP_API_TOKEN=your-whatsapp-api-token
WHATSAPP_PHONE_ID=your-phone-number-id
NOTIFY_PHONE_NUMBER=65xxxxxxxxxx    # 接收通知的电话号码（国家码+号码，无空格）

# 监听配置（可选）
MONITOR_CHECK_INTERVAL_MINS=5       # 检查间隔（分钟），默认 5
BOOKING_TIMEZONE=Asia/Singapore      # 时区，默认系统时区

# 时间窗口配置（可选）
MONITOR_WEEKDAY_START_HOUR=20        # 工作日开始时间（小时），默认 20（8pm）
MONITOR_WEEKDAY_END_HOUR=22          # 工作日结束时间（小时），默认 22（10pm）
MONITOR_WEEKEND_START_HOUR=17        # 周末开始时间（小时），默认 17（5pm）
MONITOR_WEEKEND_END_HOUR=22          # 周末结束时间（小时），默认 22（10pm）
```

## 使用

### 运行一次检查

```bash
go run . monitor.go -once
```

### 持续监听（推荐用 systemd 或 supervisor 管理）

```bash
go run . monitor.go
```

### 在后台运行（使用 nohup）

```bash
nohup go run . monitor.go > monitor.log 2>&1 &
```

### 使用 systemd 服务（Linux）

创建 `/etc/systemd/system/kallang-monitor.service`：

```ini
[Unit]
Description=Kallang Tennis Hub Monitor
After=network.target

[Service]
Type=simple
WorkingDirectory=/path/to/kallang
Environment="PATH=/usr/local/go/bin:/usr/bin"
ExecStart=/usr/bin/go run . monitor.go
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

启动服务：

```bash
sudo systemctl daemon-reload
sudo systemctl start kallang-monitor
sudo systemctl status kallang-monitor

# 启用开机自启
sudo systemctl enable kallang-monitor
```

查看日志：

```bash
sudo journalctl -u kallang-monitor -f
```

## WhatsApp 测试模式

如果你还没有完整的 WhatsApp Business Account，可以用 Meta 的测试号码：

1. 在 [Meta App Dashboard](https://developers.facebook.com/apps/) 中
2. 选择你的 App → WhatsApp → API Setup
3. 在 "Test Number" 中生成测试号码和 Token
4. 使用这个 Token 和 Phone ID 进行测试

测试号码有以下限制：
- 每天最多发送 1000 条消息
- 只能发送给已添加到白名单的号码
- 仅用于开发和测试

## 通知说明

### 收到的消息格式

```
🎾 Kallang Tennis Hub 有场地可预订！

场地: indoor Court 5 at 20:00 (PT2H)

请立即前往 PerfectGym 进行预订！
```

### 通知去重

- 同一场地的通知在 1 小时内只会发送一次
- 不同时段的同一场地会被视为不同的通知

## 常见问题

### Q: 为什么没有收到通知？

1. 检查 `.env` 文件中的 `WHATSAPP_PHONE_ID` 和 `NOTIFY_PHONE_NUMBER` 是否正确
2. 确保 WhatsApp API Token 有效
3. 查看日志中是否有错误信息：`grep "error\|Error" monitor.log`
4. 确认接收号码是否已添加到白名单（测试模式）

### Q: 如何修改检查时间间隔？

在 `.env` 中设置：
```bash
MONITOR_CHECK_INTERVAL_MINS=3  # 改为 3 分钟检查一次
```

### Q: 如何修改监听的时间窗口？

```bash
# 工作日 18:00-23:00
MONITOR_WEEKDAY_START_HOUR=18
MONITOR_WEEKDAY_END_HOUR=23

# 周末 15:00-23:00
MONITOR_WEEKEND_START_HOUR=15
MONITOR_WEEKEND_END_HOUR=23
```

### Q: 可以同时运行预订机器人和监听脚本吗？

可以，但建议：
1. 用不同的时间窗口（预订机器人用自动窗口，监听脚本用 MONITOR_* 配置）
2. 或者在不同的时段运行

## 故障排除

### 检查 PerfectGym 连接

```bash
# 测试 API 连接
DIRECT_API=true go run . -once
```

### 调整日志级别

当前脚本已使用 Go 标准 log 库，可以通过重定向查看详细日志：

```bash
go run . monitor.go 2>&1 | tee monitor.log
```

### 检查 WhatsApp 配置

运行测试发送：

```bash
# 修改 monitor.go 中的 sendAvailabilityNotification 函数
# 或者直接测试 API（用 curl）
curl -X POST https://graph.instagram.com/v20.0/YOUR_PHONE_ID/messages \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "messaging_product": "whatsapp",
    "to": "65xxxxxxxxxx",
    "type": "text",
    "text": {"body": "Test message"}
  }'
```

## 注意事项

- 脚本使用 Direct API 模式，需要有效的认证信息
- WhatsApp 通知会产生少量费用（企业账户按消息计费）
- 避免设置过短的检查间隔，以免被 API 限流
- 建议定期检查日志，确保脚本正常运行
