#!/bin/bash

# Kallang Tennis Hub 监听脚本启动器

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}🎾 Kallang Tennis Hub 场地监听${NC}"
echo "================================"

# 检查 Go 是否安装
if ! command -v go &> /dev/null; then
    echo -e "${RED}❌ Go 未安装${NC}"
    exit 1
fi

# 检查 .env 文件
if [ ! -f .env ]; then
    echo -e "${RED}❌ .env 文件不存在${NC}"
    echo -e "${YELLOW}请先运行：cp .env.monitor.example .env${NC}"
    echo -e "${YELLOW}然后编辑 .env 文件，填入你的 WhatsApp 信息${NC}"
    exit 1
fi

# 验证必需的环境变量
check_env() {
    local var=$1
    local value=$(grep "^${var}=" .env | cut -d= -f2 | tr -d ' ')
    if [ -z "$value" ] || [ "$value" = "your-"* ]; then
        echo -e "${RED}❌ 缺少配置: ${var}${NC}"
        return 1
    fi
    return 0
fi

echo -e "\n${YELLOW}验证配置...${NC}"
error=0
check_env "WHATSAPP_API_TOKEN" || error=1
check_env "WHATSAPP_PHONE_ID" || error=1
check_env "NOTIFY_PHONE_NUMBER" || error=1
check_env "PERFECTGYM_EMAIL" || error=1
check_env "PERFECTGYM_PASSWORD" || error=1

if [ $error -eq 1 ]; then
    echo -e "\n${YELLOW}请编辑 .env 文件，填入必要的配置:${NC}"
    echo "  nano .env"
    exit 1
fi

echo -e "${GREEN}✅ 配置检查通过${NC}\n"

# 检查 go.mod
if [ ! -f go.mod ]; then
    echo -e "${YELLOW}⚠️  go.mod 不存在，正在运行 go mod tidy...${NC}"
    go mod tidy
fi

# 检查认证文件
if [ ! -f .auth/storage-state.json ]; then
    echo -e "${YELLOW}⚠️  未找到 PerfectGym 认证文件${NC}"
    echo -e "${YELLOW}首次运行需要进行登录。按 Enter 继续...${NC}"
    read -p ""

    echo -e "\n${YELLOW}启动 PerfectGym 登录...${NC}"
    HEADLESS=false go run .

    if [ ! -f .auth/storage-state.json ]; then
        echo -e "${RED}❌ 登录失败或被中断${NC}"
        exit 1
    fi
    echo -e "${GREEN}✅ 登录成功${NC}\n"
fi

# 提示日志文件位置
log_file="monitor.log"
if [ "$1" != "-fg" ]; then
    echo -e "\n${YELLOW}启动监听脚本...${NC}"
    echo -e "日志文件: ${log_file}"
    echo "查看日志: tail -f ${log_file}"
    echo "停止脚本: pkill -f 'monitor.go'"
    echo ""
fi

# 运行监听脚本
if [ "$1" = "-once" ]; then
    # 一次性检查
    go run . monitor.go -once
elif [ "$1" = "-fg" ]; then
    # 前台运行（用于 systemd 等）
    go run . monitor.go
else
    # 后台运行（默认）
    nohup go run . monitor.go > "${log_file}" 2>&1 &
    pid=$!
    sleep 1

    if ! kill -0 $pid 2>/dev/null; then
        echo -e "${RED}❌ 启动失败${NC}"
        cat "${log_file}"
        exit 1
    fi

    echo -e "${GREEN}✅ 监听脚本已启动（PID: $pid）${NC}"
    echo ""
    echo -e "${YELLOW}💡 提示:${NC}"
    echo "  - 实时查看日志: tail -f ${log_file}"
    echo "  - 停止脚本: pkill -f 'monitor.go'"
    echo "  - 查询进程: ps aux | grep monitor"
fi
