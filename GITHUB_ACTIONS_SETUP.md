# 用 GitHub Actions 自动监听 - 5 分钟快速设置

**全天候监听**，每分钟自动检查一次，有空场地立即 WhatsApp 通知！

## 🎯 工作原理

- ✅ **全天候监听**：每分钟自动检查一次 Kallang Tennis Hub
- ✅ **智能通知**：只在工作日 8-10pm 或周末 5-10pm 有空场地时发送 WhatsApp 消息
- ✅ **完全免费**：GitHub Actions 每月 2000 分钟，足够用
- ✅ **无需维护**：自动运行，无需你的电脑开启

## 📋 前置条件

1. **GitHub 账户**（有仓库）
2. **WhatsApp 凭证**（从 Meta 获取）
3. **PerfectGym 账户**（邮箱和密码）

## 🚀 3 步设置

### 1️⃣ 获取 WhatsApp 凭证

**最简单：使用 Meta 测试模式**

1. 打开 [Meta App Dashboard](https://developers.facebook.com/apps/)
2. 选择你的 App（没有的话先创建）
3. 左边找 **WhatsApp** → **API Setup**
4. 找到 **Test Number** 部分，点 "Generate test credentials"
5. 复制：
   - **Phone Number ID**
   - **Auth Token**

记下这两个值。

### 2️⃣ 在 GitHub 仓库中配置 Secrets

在你的 GitHub 仓库里：

1. **Settings** → **Secrets and variables** → **Actions**
2. 点 **New repository secret**，添加以下 6 个：

| 名称 | 值 |
|------|-----|
| `WHATSAPP_API_TOKEN` | 从 Meta 获取的 token |
| `WHATSAPP_PHONE_ID` | 从 Meta 获取的 phone ID |
| `NOTIFY_PHONE_NUMBER` | 接收通知的号码（格式：65xxxxxxxxxx） |
| `PERFECTGYM_EMAIL` | 你的 PerfectGym 邮箱 |
| `PERFECTGYM_PASSWORD` | 你的 PerfectGym 密码 |
| `PERFECTGYM_BASE_URL` | PerfectGym Booking 页面 URL |

**PERFECTGYM_BASE_URL 获取方法**：
1. 打开 PerfectGym Booking 页面
2. 复制浏览器地址栏的完整 URL，例如：
   ```
   https://thekallang.perfectgym.com/clientportal2/#/FacilityBooking?clubId=1
   ```

**NOTIFY_PHONE_NUMBER 格式**：
```
新加坡号码：65xxxxxxxxxx（无空格、无 + 号）
例如：659xxxxxxx
```

### 3️⃣ Push GitHub Actions 配置

1. **复制** `.github/workflows/monitor.yml` 到你的仓库根目录
2. **提交并 Push**：
   ```bash
   git add .github/workflows/monitor.yml
   git commit -m "Add GitHub Actions monitor"
   git push
   ```

**完成！** 🎉

---

## ✅ 验证是否工作

1. 打开仓库的 **Actions** 标签
2. 应该看到 "Kallang Tennis Monitor" workflow
3. 点开查看运行日志

看到类似输出表示成功：
```
Checking at 2026-09-04T20:15:30+08:00 (local time)
Found 1 available slot(s)
Sent WhatsApp notification for: indoor Court 5 at 20:00
```

---

## 📱 监听时间说明

**脚本会全天候检查**，但只在以下时间段有空场地时发送通知：

- 🔔 **工作日**（周一-五）：**晚 8 点到 10 点**
- 🔔 **周末**（周六-日）：**下午 5 点到 10 点**

### 修改通知时间窗口

编辑 `.github/workflows/monitor.yml`，修改这些行：

```yaml
MONITOR_WEEKDAY_START_HOUR: 20        # 改为你想要的时间（24小时制）
MONITOR_WEEKDAY_END_HOUR: 22
MONITOR_WEEKEND_START_HOUR: 17
MONITOR_WEEKEND_END_HOUR: 22
```

例如改成全天通知：
```yaml
MONITOR_WEEKDAY_START_HOUR: 0
MONITOR_WEEKDAY_END_HOUR: 24
MONITOR_WEEKEND_START_HOUR: 0
MONITOR_WEEKEND_END_HOUR: 24
```

然后 push：
```bash
git add .github/workflows/monitor.yml
git commit -m "Update monitor time window"
git push
```

---

## 🐛 故障排除

### 没有收到 WhatsApp 消息？

**检查 1：验证号码格式**
```
❌ +65 9xxx xxxx     （有 +，有空格）
❌ 65-9xxx-xxxx     （有 -）
✅ 659xxxxxxxx      （无空格、无符号）
```

**检查 2：在 GitHub Actions 日志中查看错误**
1. **Actions** → **Kallang Tennis Monitor**
2. 点最新的运行记录
3. 展开 "Run monitor check" 步骤
4. 搜索 "whatsapp" 或 "error"

**检查 3：测试 WhatsApp Token**

在 GitHub Actions 日志中看到类似错误：
```
whatsapp api returned 401: Invalid token
```

说明 Token 已过期，需要重新获取。

### 显示 "Outside target time window"？

说明当前时间不在通知窗口内。这是正常的。

脚本仍在检查，但发现空场地时不会通知（因为不在时间范围内）。

### GitHub Actions 显示红色 ❌？

查看日志找出原因：
- 通常是 PerfectGym 密码错误
- 或者 PERFECTGYM_BASE_URL 格式不对

---

## 💡 高级配置

### 修改检查频率

目前每分钟检查一次（`* * * * *`）。

如果想改成每 2 分钟：
```yaml
- cron: '*/2 * * * *'    # 每 2 分钟
```

每 5 分钟：
```yaml
- cron: '*/5 * * * *'    # 每 5 分钟
```

### 跳过周末或工作日

只监听工作日：
```yaml
on:
  schedule:
    - cron: '* * * * 1-5'   # 周一到周五
```

只监听周末：
```yaml
on:
  schedule:
    - cron: '* * * * 0,6'   # 周六和周日
```

### 加速检查（使用多线程）

编辑 `.github/workflows/monitor.yml`：
```yaml
DIRECT_API_WORKERS: 4     # 改为更大的数字（最多 16）
```

---

## 📊 成本和配额

**GitHub Actions 免费额度**：
- ✅ 每月 2000 分钟
- ✅ 如果每分钟运行一次：2000 分钟 / 1 分钟 = 每月 2000 次检查
- ✅ 足够了！

**计算**：
- 每分钟运行：24 小时 × 60 分钟 × 30 天 = 43,200 分钟
- 超过了！

**建议**：
改成每 2-3 分钟运行一次会更划算：
```yaml
- cron: '*/3 * * * *'    # 每 3 分钟
```

这样：每月 14,400 分钟（在配额内）

---

## 🎓 常见问题

### Q: 为什么不能每秒检查一次？

GitHub Actions 的 cron 最小粒度是分钟（`* * * * *` = 每分钟）。

如果需要秒级别的检查，需要在云服务器上自己部署。

### Q: 万一我改了 PerfectGym 密码怎么办？

1. 在 GitHub Secrets 中更新密码
2. 下次运行时会自动用新密码

### Q: 能在手机上看日志吗？

可以！在 GitHub 的 Actions 标签里直接看。

### Q: 脚本会暴露我的密码吗？

不会！GitHub Secrets 是加密的，日志中也看不到密码。

---

## ✨ 下一步

现在你有了一个全天候的监听系统！

**推荐**：
1. 在 GitHub 中 star 这个仓库（提醒自己）
2. 定期检查 Actions 日志，看是否有通知
3. 收到 WhatsApp 通知后立即去 PerfectGym 预订

**有问题**？
- 检查 GitHub Actions 的日志
- 确保所有 Secrets 都填正确了
- 在 Discord/GitHub Issues 提问

祝你抢到场地！🎾
