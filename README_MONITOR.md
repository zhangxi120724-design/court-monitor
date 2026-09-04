# 🎾 Kallang Tennis Hub 全天候场地监听

**每分钟自动检查一次**，有空场地立即 WhatsApp 通知你！

> 💡 不需要你的电脑开启，完全免费，自动运行！

---

## ⚡ 快速开始（2 分钟）

### 用 GitHub Actions（推荐 ⭐）

**最简单的方法，完全免费！**

1️⃣ **获取 WhatsApp 凭证**（5 分钟）

- 打开 [Meta App Dashboard](https://developers.facebook.com/apps/)
- 找到 **WhatsApp** → **API Setup**
- 在 "Test Number" 中点 "Generate test credentials"
- 复制 **Phone Number ID** 和 **Auth Token**

2️⃣ **在 GitHub 添加 Secrets**

仓库 → **Settings** → **Secrets and variables** → **Actions** → **New repository secret**

添加这 6 个：
```
WHATSAPP_API_TOKEN       = <从 Meta 获取>
WHATSAPP_PHONE_ID        = <从 Meta 获取>
NOTIFY_PHONE_NUMBER      = 65xxxxxxxxxx
PERFECTGYM_EMAIL         = <你的邮箱>
PERFECTGYM_PASSWORD      = <你的密码>
PERFECTGYM_BASE_URL      = <你的 booking URL>
```

3️⃣ **Push 配置**

```bash
git add .github/workflows/monitor.yml
git commit -m "Add monitor"
git push
```

**完成！** ✅ 现在每分钟自动检查一次。

---

## 📖 详细文档

根据你的需求选择：

| 需求 | 文档 |
|------|------|
| **用 GitHub Actions 全自动** | [GITHUB_ACTIONS_SETUP.md](GITHUB_ACTIONS_SETUP.md) |
| **自己的云服务器** | [MONITOR.md](MONITOR.md) |
| **快速本地测试** | [MONITOR_QUICKSTART.md](MONITOR_QUICKSTART.md) |
| **想用启动脚本** | `./start-monitor.sh -once` |

---

## 🔔 监听时间说明

脚本**全天候检查**，但只在以下时间有可用场地时通知你：

- 📅 **工作日**（周一-五）：晚 **8-10 点**
- 📅 **周末**（周六-日）：下午 **5-10 点**

### 修改通知时间

编辑 `.github/workflows/monitor.yml`：

```yaml
MONITOR_WEEKDAY_START_HOUR: 20    # 改为你想要的小时
MONITOR_WEEKDAY_END_HOUR: 22
MONITOR_WEEKEND_START_HOUR: 17
MONITOR_WEEKEND_END_HOUR: 22
```

或者编辑 `.env`：
```bash
MONITOR_WEEKDAY_START_HOUR=18
MONITOR_WEEKDAY_END_HOUR=23
MONITOR_WEEKEND_START_HOUR=15
MONITOR_WEEKEND_END_HOUR=23
```

---

## 💬 收到的通知

```
🎾 Kallang Tennis Hub 有场地可预订！

场地: indoor Court 5 at 20:00 (PT2H)

请立即前往 PerfectGym 进行预订！
```

---

## ❓ 常见问题

### 为什么收不到消息？

1. **检查号码格式**
   ```
   ✅ 659xxxxxxxx    （正确）
   ❌ +65 9xxx xxxx  （错误）
   ❌ 65-9xxx-xxxx   （错误）
   ```

2. **检查 GitHub Actions 日志**
   - 仓库 → **Actions** → 最新运行 → 查看日志

3. **检查 WhatsApp Token 是否过期**
   - 在 Meta Dashboard 重新生成

### 检查频率可以改吗？

可以！编辑 `.github/workflows/monitor.yml`：

```yaml
- cron: '*/2 * * * *'   # 改成每 2 分钟
- cron: '*/5 * * * *'   # 改成每 5 分钟
```

记住 GitHub Actions 免费额度是每月 2000 分钟。

### 密码会不会泄露？

不会！GitHub Secrets 是加密的，日志中看不到密码。

### 脚本什么时候停止？

永远不会！除非你删除 workflow 文件或仓库。

---

## 📊 监听的场地

- ✅ **Indoor**（1-7 号）
- ✅ **Outdoor**（8-15 号）
- ✅ **自动优先级**：Indoor 优先，Outdoor 备选
- ✅ **优先时长**：120 分钟优先，60 分钟备选

---

## 🆘 需要帮助？

1. **本地测试**：`./start-monitor.sh -once`
2. **查看日志**：
   - GitHub Actions：仓库 → Actions → 最新运行
   - 本地：`tail -f monitor.log`
3. **检查配置**：确保所有 Secrets 和 `.env` 变量都填正确了

---

## 📝 配置清单

使用 GitHub Actions 需要的清单：

- [ ] 有 GitHub 仓库
- [ ] 获取了 WhatsApp Token 和 Phone ID
- [ ] 在 GitHub Secrets 中添加了 6 个环境变量
- [ ] Push 了 `.github/workflows/monitor.yml`
- [ ] 在 GitHub Actions 中看到了运行记录
- [ ] 收到了测试的 WhatsApp 消息

---

## 🎯 下一步

1. ✅ 完成上面的快速开始（2 分钟）
2. ✅ 在 GitHub Actions 看到运行成功
3. ✅ 收到第一条 WhatsApp 通知
4. ✅ 有空场地时自动收到提醒
5. ✅ 立即去 PerfectGym 预订

祝你抢到场地！🎾
