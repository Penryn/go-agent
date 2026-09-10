#!/bin/bash
# 记忆学习系统重构部署脚本
# 版本: v1
# 日期: 2026-09-10

set -e  # 遇到错误立即退出

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 日志函数
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查环境变量
check_env() {
    log_info "检查环境变量..."

    if [ -z "$DATABASE_URL" ]; then
        log_error "DATABASE_URL 未设置"
        exit 1
    fi

    log_info "✅ 环境变量检查通过"
}

# 运行测试
run_tests() {
    log_info "运行所有测试..."

    if go test ./... -v; then
        log_info "✅ 所有测试通过"
    else
        log_error "❌ 测试失败，停止部署"
        exit 1
    fi
}

# 备份数据库
backup_database() {
    log_info "备份数据库..."

    BACKUP_FILE="backups/backup_$(date +%Y%m%d_%H%M%S).sql"
    mkdir -p backups

    if pg_dump "$DATABASE_URL" > "$BACKUP_FILE"; then
        log_info "✅ 数据库备份完成: $BACKUP_FILE"
    else
        log_error "❌ 数据库备份失败"
        exit 1
    fi
}

# 执行数据库迁移
run_migration() {
    log_info "执行数据库迁移..."

    if psql "$DATABASE_URL" < schema/migrations/001_memory_refactor.sql; then
        log_info "✅ 数据库迁移完成"
    else
        log_error "❌ 数据库迁移失败"
        log_warn "请手动恢复备份: psql \$DATABASE_URL < $BACKUP_FILE"
        exit 1
    fi
}

# 验证迁移
verify_migration() {
    log_info "验证数据库迁移..."

    TABLES=("memories" "memory_evidence" "memory_changes" "learning_event_progress")

    for table in "${TABLES[@]}"; do
        if psql "$DATABASE_URL" -c "\d $table" > /dev/null 2>&1; then
            log_info "✅ 表 $table 存在"
        else
            log_error "❌ 表 $table 不存在"
            exit 1
        fi
    done

    log_info "✅ 数据库迁移验证通过"
}

# 构建应用
build_app() {
    log_info "构建应用..."

    if go build -o bin/agent ./cmd/agent; then
        log_info "✅ 应用构建完成"
    else
        log_error "❌ 应用构建失败"
        exit 1
    fi
}

# 主函数
main() {
    log_info "===== 开始部署记忆学习系统重构 ====="
    log_info "版本: v1"
    log_info "时间: $(date)"
    echo ""

    # 确认部署
    read -p "确认开始部署？这将停止服务并执行数据库迁移。(yes/no): " confirm
    if [ "$confirm" != "yes" ]; then
        log_warn "部署已取消"
        exit 0
    fi

    echo ""

    # 执行部署步骤
    check_env
    run_tests
    backup_database

    # 最后确认
    echo ""
    log_warn "⚠️  即将执行数据库迁移（破坏性变更）"
    read -p "确认继续？(yes/no): " confirm2
    if [ "$confirm2" != "yes" ]; then
        log_warn "部署已取消"
        exit 0
    fi

    run_migration
    verify_migration
    build_app

    echo ""
    log_info "===== 部署完成 ====="
    log_info "✅ 所有步骤成功完成"
    echo ""
    log_info "下一步："
    log_info "1. 启动服务: systemctl start agent"
    log_info "2. 查看日志: journalctl -u agent -f"
    log_info "3. 验证 API: curl http://localhost:8080/health"
    echo ""
}

# 运行主函数
main
