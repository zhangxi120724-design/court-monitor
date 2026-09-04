# Kallang Tennis Hub 场地监听 - 快速入门

只需 3 步就能启动场地监听和 WhatsApp 通知！

## 步骤 1：获取 WhatsApp 凭证

### 选项 A：使用 WhatsApp 测试模式（推荐新手）

1. 打开 [Meta App Dashboard](https://developers.facebook.com/apps/)
2. 选择你的 App（没有的话先创建一个）
3. 左边菜单找到 **WhatsApp** → **API Setup**
4. 找到 **Test Number** 部分，点击 "Generate test credentials"
5. 你会看到：
   - **Phone Number ID**: `123456789...`
   - **Auth Token**: `EAA...`
6. 复制这两个值

### 选项 B：使用正式的 WhatsApp Business Account

1. 申请 [WhatsApp Business Account](https://www.whatsapp.com/business/)
2. 按照 Meta 的步骤完成设置
3. 在 Business Manager 中找到 Phone Number ID 和 Access Token

## 步骤 2：配置环境变量

```bash
# 复制示例配置
cp .env.monitor.example .env

# 编辑 .env，填入你的信息
nano .env
```

关键配置项：

```bash
# 你的 PerfectGym 账户
PERFECTGYM_EMAIL=your-email@example.com
PERFECTGYM_PASSWORD=your-password

# WhatsApp（从步骤 1 获取）
WHATSAPP_PHONE_ID=123456789...
WHATSAPP_API_TOKEN=EAA...

# 接收通知的手机号（新加坡格式）
NOTIFY_PHONE_NUMBER=659xxxxxxxx   # 用你的真实号码替换 x
```

## 步骤 3：首次运行测试

```bash
# 一次性检查，看是否工作
go run . monitor.go -once
```

如果一切正常，你会看到：
```
2026-09-04T20:15:30.123456Z Checking at 2026-09-04T20:15:30+08:00 (local time)
2026-09-04T20:15:31.234567Z Outside target time window...
```

### 测试发送 WhatsApp 消息

为了确保 WhatsApp 能正常工作，可以手动测试：

```bash
# 编辑 monitor.go，在 main 函数末尾临时加入：
cfg, _ := loadMonitorConfig()
msg := whatsappMessage{
    MessagingProduct: "whatsapp",
    To:               cfg.NotifyPhoneNumber,
    Type:             "text",
    Text: whatsappTextContent{
        Body: "🎾 测试消息 - Kallang Tennis Hub 监听已启动！",
    },
}
sendWhatsAppMessage(cfg, msg)
```

然后运行：
```bash
go run . monitor.go -once
```

你应该会在 WhatsApp 上收到测试消息。

## 步骤 4：启动长期监听

```bash
# 后台运行（Ctrl+C 可停止）
go run . monitor.go

# 或者用 nohup 完全后台运行
nohup go run . monitor.go > monitor.log 2>&1 &

# 查看日志
tail -f monitor.log
```

## 时间窗口说明

默认设置：
- 🔔 **工作日**（周一-五）：**晚上 8-10 点**检查
- 🔔 **周末**（周六-日）：**下午 5-10 点**检查

如果你想改时间，在 `.env` 中修改：

```bash
# 改成工作日 18:00-23:00
MONITOR_WEEKDAY_START_HOUR=18
MONITOR_WEEKDAY_END_HOUR=23

# 改成周末全天（0-24）
MONITOR_WEEKEND_START_HOUR=0
MONITOR_WEEKEND_END_HOUR=24
```

## 常见问题快速解决

### 收不到 WhatsApp 消息？

1. **检查号码格式**：必须是 `65` + 数字，无空格无 `+`
   ```
   ❌ +65 9xxx xxxx
   ❌ 65-9xxx-xxxx
   ✅ 659xxxxxxxx
   ```

2. **检查 Token 有效性**：在 Meta Dashboard 中验证 Token 是否还活跃

3. **检查白名单**（测试模式）：在 "Recipient Phone Numbers" 中确保你的号码已添加

4. **查看详细错误**：
   ```bash
   grep "whatsapp" monitor.log
   ```

### 显示 "Outside target time window"？

脚本只在特定时间窗口检查。如果你想测试其他时间：

```bash
# 临时改时间窗口进行测试
MONITOR_WEEKDAY_START_HOUR=0 MONITOR_WEEKDAY_END_HOUR=24 go run . monitor.go -once
```

### 需要重新登录 PerfectGym？

```bash
# 清除旧的登录态
rm -rf .auth

# 重新登录一次
HEADLESS=false PERFECTGYM_EMAIL=your-email PERFECTGYM_PASSWORD=your-password go run .

# 然后启动监听
go run . monitor.go
```

## 下一步

✅ 脚本现在正在运行！

### 推荐进一步配置：

1. **用 systemd 管理**（Linux）：
   ```bash
   sudo cp -r . /opt/kallang-monitor
   sudo nano /etc/systemd/system/kallang-monitor.service
   # 参考 MONITOR.md 中的 systemd 配置
   ```

2. **用 crontab 定时启动**（如果需要开关）：
   ```bash
   crontab -e
   # 早上 6 点启动，晚上 11 点停止
   ```

3. **监控脚本运行状态**：
   ```bash
   # 定期检查进程是否还活着
   ps aux | grep monitor
   ```

## 获取帮助

- 查看详细文档：`MONITOR.md`
- 检查日志：`tail -f monitor.log`
- 调试 API 连接：`DIRECT_API=true go run . -once`
