# perfectgym-booking-bot

一个独立的 Playwright 小仓库，用来打开 PerfectGym 的 Facility Booking 页面，定位 `indoor` 场地并尝试加入购物车。

## 能做什么

- 复用登录态，避免每次都重新登录
- 跳转到指定 Facility Booking 页面
- 按日期、时间、场地关键词筛选候选 slot
- 找到可预约项后点击加入购物车
- 在放号前持续轮询

## 使用方式

1. 安装依赖

```bash
npm install
npx playwright install chromium
```

2. 复制环境变量

```bash
cp .env.example .env
```

3. 首次运行建议可视化模式，方便人工确认登录流程和页面文案

```bash
npm run book:headed
```

4. 后续如果登录态已缓存，可以直接运行

```bash
npm run book
```

## 说明

- 如果页面登录表单字段和脚本默认识别的不一致，先手动登录一次即可，脚本会把登录态保存到 `.auth/storage-state.json`
- `TARGET_TIME_TEXT` 和 `TARGET_COURT_KEYWORD` 都是模糊匹配，适合页面文案有轻微变化的情况
- 由于 PerfectGym 是前端渲染站点，具体按钮文本可能和当前版本略有不同；如果你实际打开页面后发现按钮文案不同，改一下 `src/book-facility.js` 里的候选选择器即可
