# perfectgym-booking-bot

一个用 Go + Playwright 写的抢场地脚本，用来打开 PerfectGym 的 Facility Booking 页面，定位目标时段和场地，并在开抢时间到达后自动加入购物车。

默认策略已经按你这类规则处理：

- 先抢 `indoor` tab，对应 `zoneTypeId=85`
- 如果 `indoor` 没有，再抢 `outdoor` tab，对应 `zoneTypeId=83`
- `outdoor` 只接受 `8-15` 号场
- 时长优先级是 `120 分钟`，如果下一小时不可选，再退回 `60 分钟`
- 如果没显式设置 `BOOKING_START_AT`，脚本会按“目标日期同一时段，往前推 7 天”自动算开抢时间
- 到达开抢点后不会高频死刷，而是只做有限次 burst 刷新

## 能做什么

- 复用登录态，避免每次都重新登录
- 同时盯住 indoor / outdoor 两个 booking 页
- 按日期、时间和场地优先级筛选候选 slot
- 在目标开抢时间之前提前打开浏览器并完成登录
- 到达 `BOOKING_START_AT` 后按有限次数刷新并尝试加入购物车
- Direct API 模式可用 `DIRECT_API_EARLY_MS` 提前几毫秒开始轮询

## 使用方式

1. 安装 Go 依赖

```bash
go mod tidy
```

2. 安装 Playwright driver 和 Chromium

```bash
go run github.com/playwright-community/playwright-go/cmd/playwright@v0.4702.0 install chromium
```

3. 复制环境变量

```bash
cp .env.example .env
```

4. 首次运行建议可视化模式，方便人工确认登录流程和页面文案

```bash
HEADLESS=false go run .
```

5. 后续如果登录态已缓存，可以直接运行

```bash
HEADLESS=true go run .
```

## 定时开抢

如果你想手动指定开抢时间，可以在 `.env` 里设置：

```bash
BOOKING_START_AT=2026-04-27 20:00:00
BOOKING_TIMEZONE=Asia/Singapore
BOOKING_PREPARE_SECONDS=90
```

脚本行为会变成：

- 先等待到 `BOOKING_START_AT - BOOKING_PREPARE_SECONDS`
- 提前打开浏览器、恢复登录态并进入目标页面
- 到达 `BOOKING_START_AT` 后才正式开始刷新和点击
- 默认只刷 `4` 轮，每轮间隔 `100ms`

如果你不填 `BOOKING_START_AT`，脚本会按下面的规则自动算：

- 目标日期来自 `TARGET_DATE`，或默认 `TARGET_OFFSET_DAYS=7`
- 开抢时间取 `TARGET_TIME_TEXT`
- 最终开抢点 = `目标日期 + 目标时间 - 7 天`

例如：

```bash
TARGET_OFFSET_DAYS=7
TARGET_TIME_TEXT=20:00
AUTO_BOOKING_WINDOW=true
BOOKING_OPEN_LEAD_DAYS=7
```

这时脚本会把开抢时间推成“今天 20:00”，正好对应“今天同一时间抢下周同一时段”。

## 关键环境变量

- `PERFECTGYM_BASE_URL`: 目标 Facility Booking 页面
- `INDOOR_BOOKING_URL`: 可选，默认从 `PERFECTGYM_BASE_URL` 推成 `zoneTypeId=85`
- `OUTDOOR_BOOKING_URL`: 可选，默认从 `PERFECTGYM_BASE_URL` 推成 `zoneTypeId=83`
- `TARGET_DATE` / `TARGET_OFFSET_DAYS`: 想抢的日期
- `TARGET_TIME_TEXT`: 例如 `19:00`
- `OUTDOOR_COURT_PRIORITY`: 默认 `8-15`
- `BOOKING_DURATION_PRIORITY`: 默认 `120,60`
- `REFRESH_ATTEMPTS`: 默认 `4`
- `REFRESH_SPACING_MS`: 默认 `100`
- `DIRECT_API_EARLY_MS`: Direct API 模式提前多少毫秒开始第一次尝试，默认 `0`
- `DIRECT_API_WORKERS`: Direct API 模式每轮并发检查多少个场地 schedule，默认 `1`
- `BOOKING_START_AT`: 可选的定时开抢时间
- `AUTO_BOOKING_WINDOW`: 是否自动按“目标日期减 7 天同一时段”推开抢时间
- `BOOKING_OPEN_LEAD_DAYS`: 默认 `7`
- `BOOKING_PREPARE_SECONDS`: 提前预热秒数

## 说明

- 如果页面登录表单字段和脚本默认识别的不一致，先手动登录一次即可，脚本会把登录态保存到 `.auth/storage-state.json`
- `TARGET_TIME_TEXT` 和 `TARGET_COURT_KEYWORD` 仍然是模糊匹配，适合页面文案有轻微变化的情况
- 由于 PerfectGym 是前端渲染站点，具体按钮文本可能和当前版本略有不同；如果你实际打开页面后发现按钮文案不同，改一下 [main.go](/Users/xi.zhang/go/src/kallang/main.go) 里的候选选择器即可

## Troubleshooting

- 如果 `go build` 提示 `no such tool "compile"`，说明当前 shell 命中的 Go 不是完整工具链；在这台机器上可以改用 `/usr/local/go/bin/go` 运行上述命令
