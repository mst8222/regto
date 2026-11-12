// cmd/migrate：数据库迁移 CLI（ROADMAP §3.3 迁移规范）。
//
// 用法：
//
//	migrate up [n]     应用全部（或前 n 个）未执行的迁移
//	migrate down [n]   回滚最近 1 个（或 n 个）已执行的迁移；down all = 回滚到空库
//	migrate force <v>  人工修复 dirty：把 schema_migrations 置为版本 v 且 dirty=false
//	migrate version    打印当前版本与 dirty 状态
//	migrate validate   校验迁移文件可加载、版本链连续、非 dirty
//
// ⚠️ 历史教训：无参数的 down 会一路回滚到空库（曾误把 plans/releases/regto_import
// 全部 DROP）。因此 down 默认只回滚 1 步，全量回滚必须显式写 `down all`。
//
// 连接：优先 REGTO_MIGRATION_URL，回退 REGTO_DATABASE_URL。
// 生产禁止自动迁移：server 启动只校验 schema_version，迁移必须由本命令显式执行（AGENTS.md §5）。
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/mst8222/regto/internal/platform/config"
	"github.com/mst8222/regto/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(exitCode(err))
	}
}

// exitCode：区分「正常无变更」（1）与「真错误」（2），便于 CI 判定。
func exitCode(err error) int {
	if errors.Is(err, migrate.ErrNoChange) {
		return 1
	}
	return 2
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("缺少子命令：up|down|version|validate")
	}
	cmd := args[0]

	url := dbURL()
	if url == "" {
		return errors.New("未设置 REGTO_MIGRATION_URL 或 REGTO_DATABASE_URL")
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("加载内嵌迁移文件失败: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, url)
	if err != nil {
		return fmt.Errorf("初始化 migrate 失败: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	switch cmd {
	case "up":
		if n, ok := parseSteps(args); ok {
			return m.Steps(n)
		}
		return m.Up()
	case "down":
		if len(args) >= 2 && strings.EqualFold(strings.TrimSpace(args[1]), "all") {
			return m.Down() // 显式要求才全量回滚（危险）
		}
		if n, ok := parseSteps(args); ok {
			// down 的步数取负值语义由 Steps 处理（负号=回退）。
			return m.Steps(-n)
		}
		// 默认只回滚 1 步：避免无参数 down 一路 DROP 到空库。
		return m.Steps(-1)
	case "force":
		if len(args) < 2 {
			return errors.New("force 需要版本号参数：migrate force <version>")
		}
		v, err := strconv.Atoi(strings.TrimSpace(args[1]))
		if err != nil || v < 0 {
			return fmt.Errorf("非法的版本号 %q", args[1])
		}
		// force 只改 golang-migrate 的元数据，**不执行任何 SQL**。
		// 用途：迁移在半途失败留下 dirty 时，人工确认物理状态后把游标拨回真实版本。
		return m.Force(v)
	case "version":
		v, dirty, err := m.Version()
		if err != nil {
			return err
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
		return nil
	case "validate":
		return validate(m, src)
	default:
		return fmt.Errorf("未知子命令 %q：up|down|version|validate", cmd)
	}
}

// validate：版本链连续性 + 非 dirty + 文件可解析。
// 版本链连续 = FirstVersion==1 且相邻版本步进为 1。
func validate(m *migrate.Migrate, src source.Driver) error {
	v, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return err
	}
	if errors.Is(err, migrate.ErrNilVersion) {
		v = 0 // 空库：合法起点
	}
	if dirty {
		return fmt.Errorf("数据库处于 dirty 状态（version=%d）：需人工修复后才能继续", v)
	}
	first, err := src.First()
	if err != nil {
		return fmt.Errorf("读取迁移文件失败: %w", err)
	}
	if first != 1 {
		return fmt.Errorf("迁移文件必须从版本 1 开始，实际首版本=%d", first)
	}
	// 从首版本 1 起向后遍历（source.Migrations.Next(0) 无语义，必须传已注册的版本）。
	prev, last := first, first
	for {
		next, err := src.Next(prev)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				break // 已遍历到最后一个版本
			}
			return fmt.Errorf("遍历迁移文件失败: %w", err)
		}
		if next != prev+1 {
			return fmt.Errorf("版本链断裂：%d 之后是 %d（迁移序号必须全局单调连续）", prev, next)
		}
		prev, last = next, next
	}
	fmt.Printf("validate OK: 版本链连续 1..%d，当前库版本=%d，非 dirty\n", last, v)
	return nil
}

// parseSteps：解析可选的步数参数（up 3 / down 3）。
func parseSteps(args []string) (int, bool) {
	if len(args) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(args[1]))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// dbURL：优先 REGTO_MIGRATION_URL（超级用户），回退 REGTO_DATABASE_URL。
// 先加载 .env（真实环境变量优先，不被覆盖），保证本地一条命令可跑。
func dbURL() string {
	_ = config.LoadDotEnv(".env")
	if u := strings.TrimSpace(os.Getenv("REGTO_MIGRATION_URL")); u != "" {
		return u
	}
	return strings.TrimSpace(os.Getenv("REGTO_DATABASE_URL"))
}
