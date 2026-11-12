package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mst8222/regto/internal/adapter/persistence/postgres"
	daccount "github.com/mst8222/regto/internal/domain/account"
	pauthn "github.com/mst8222/regto/internal/platform/authn"
	"github.com/mst8222/regto/internal/platform/config"
	"github.com/mst8222/regto/internal/platform/db"
	ucaccount "github.com/mst8222/regto/internal/usecase/account"
)

// accountCmd 处理 `regtoctl account ...`（SPEC-ACCOUNT-001）。
//
// 为什么 CLI 也要有：服务器没起、或人类还没拿到 JWT 时，仍然需要能
// 「看一眼矩阵」「给某人绑个角色」——尤其是首装时的一次性 init-admin。
func accountCmd(args []string) error {
	if len(args) == 0 {
		fmt.Print(`用法：
  regtoctl account matrix [--role qa] [--output text|json]   打印权限矩阵（不需要数据库）
  regtoctl account roles                                     列出角色（需要数据库）
  regtoctl account user --user N                             查看某用户的有效权限
  regtoctl account assign --user N --role pm [--by N]         绑定角色
  regtoctl account revoke --user N --role pm                  解绑角色
  regtoctl account init-admin --account admin --password *** [--realname 名字]
                                                             建用户（若无）+ 绑 admin
`)
		return nil
	}
	switch args[0] {
	case "matrix":
		return accountMatrixCmd(args[1:])
	case "roles":
		return withAccountService(func(ctx context.Context, svc *ucaccount.Service) error {
			metas, err := svc.Roles(ctx)
			if err != nil {
				return err
			}
			for _, m := range metas {
				builtin := "自定义"
				if m.Builtin {
					builtin = "内置"
				}
				fmt.Printf("%-6s %-8s [%s] %s\n", m.Code, m.Name, builtin, m.Description)
			}
			return nil
		})
	case "user":
		fs := flag.NewFlagSet("account user", flag.ContinueOnError)
		userID := fs.Int64("user", 0, "用户 ID")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *userID <= 0 {
			return fmt.Errorf("需要 --user N")
		}
		return withAccountService(func(ctx context.Context, svc *ucaccount.Service) error {
			roles, perms, err := svc.Effective(ctx, *userID)
			if err != nil {
				return err
			}
			if len(roles) == 0 {
				fmt.Printf("用户 %d 没有任何角色（enforce 模式下所有请求都会被 403）\n", *userID)
				return nil
			}
			fmt.Printf("用户 %d 的角色：%s\n", *userID, roleList(roles))
			printPermissionTable(perms)
			return nil
		})
	case "assign":
		fs := flag.NewFlagSet("account assign", flag.ContinueOnError)
		userID := fs.Int64("user", 0, "用户 ID")
		roleCode := fs.String("role", "", "角色 code：admin|pm|dev|qa|guest")
		by := fs.Int64("by", 0, "授予人 user_id（可空）")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		role, err := parseRoleFlag(*roleCode)
		if err != nil {
			return err
		}
		return withAccountService(func(ctx context.Context, svc *ucaccount.Service) error {
			if err := svc.Bind(ctx, *userID, role, *by); err != nil {
				return err
			}
			fmt.Printf("已绑定：user=%d role=%s（幂等：重复执行不新增行）\n", *userID, role)
			return nil
		})
	case "revoke":
		fs := flag.NewFlagSet("account revoke", flag.ContinueOnError)
		userID := fs.Int64("user", 0, "用户 ID")
		roleCode := fs.String("role", "", "角色 code")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		role, err := parseRoleFlag(*roleCode)
		if err != nil {
			return err
		}
		return withAccountService(func(ctx context.Context, svc *ucaccount.Service) error {
			if err := svc.Unbind(ctx, *userID, role); err != nil {
				return err
			}
			fmt.Printf("已解绑（软删）：user=%d role=%s\n", *userID, role)
			return nil
		})
	case "init-admin":
		return accountInitAdminCmd(args[1:])
	default:
		return fmt.Errorf("未知 account 子命令 %q", args[0])
	}
}

// accountMatrixCmd 打印矩阵。不需要数据库——矩阵是代码真源（DEC-01）。
func accountMatrixCmd(args []string) error {
	fs := flag.NewFlagSet("account matrix", flag.ContinueOnError)
	only := fs.String("role", "", "只看某个角色")
	format := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	roles := daccount.AllRoles()
	if *only != "" {
		role, err := parseRoleFlag(*only)
		if err != nil {
			return err
		}
		roles = []daccount.Role{role}
	}

	if *format == "json" {
		out := map[string][]daccount.Permission{}
		for _, role := range roles {
			out[string(role)] = daccount.PermissionsOf(role)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	// 表格：每行一个角色，每列一个资源，格子里是 VCED 掩码（·=拒绝）。
	fmt.Printf("%-6s", "角色")
	for _, res := range daccount.AllResources() {
		fmt.Printf("\t%-8s", res)
	}
	fmt.Println()
	for _, role := range roles {
		fmt.Printf("%-6s", role)
		for _, res := range daccount.AllResources() {
			fmt.Printf("\t%-8s", maskBadge(role, res))
		}
		fmt.Println()
	}
	fmt.Println("\nV=view C=create E=edit D=delete，·=拒绝；trace 的 E/D 为物理禁止（迁移 000019）")
	return nil
}

// maskBadge 生成 VCED 掩码（基于 Authorize，与真实判定同源）。
func maskBadge(role daccount.Role, res daccount.Resource) string {
	var b strings.Builder
	for _, act := range daccount.AllActions() {
		if daccount.Authorize([]daccount.Role{role}, res, act).Allowed {
			b.WriteByte(strings.ToUpper(act.String())[0])
		} else {
			// 必须 WriteString：'·' 是 3 字节，WriteByte 会把 rune 截断成 0xB7 变乱码。
			b.WriteString("·")
		}
	}
	return b.String()
}

// accountInitAdminCmd 初始化首个管理员：建用户（若无）+ 绑 admin。
//
// 这是「自锁」的唯一出口。密码用 bcrypt 存哈希，明文不进日志、不进 shell history
// 之外的任何地方（提示：生产环境请用交互式输入或环境变量，避免留在 history 里）。
func accountInitAdminCmd(args []string) error {
	fs := flag.NewFlagSet("account init-admin", flag.ContinueOnError)
	account := fs.String("account", "", "登录名（唯一）")
	password := fs.String("password", os.Getenv("REGTO_INIT_PASSWORD"), "初始密码（也可用环境变量 REGTO_INIT_PASSWORD）")
	realname := fs.String("realname", "", "真实姓名（可空）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	acc := strings.TrimSpace(*account)
	if acc == "" {
		return fmt.Errorf("需要 --account（登录名）")
	}
	if len(*password) < 8 {
		return fmt.Errorf("初始密码至少 8 位（或用 REGTO_INIT_PASSWORD 传入）")
	}

	ctx := context.Background()
	cfg, err := config.Load(0)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer pool.Close()

	hash, err := pauthn.HashPassword(*password)
	if err != nil {
		return fmt.Errorf("生成密码哈希失败: %w", err)
	}

	repo := postgres.NewAccountRepo(pool)
	userID, created, err := repo.EnsureUser(ctx, acc, hash, strings.TrimSpace(*realname))
	if err != nil {
		return err
	}
	svc := ucaccount.NewService(repo)
	if err := svc.Bind(ctx, userID, daccount.RoleAdmin, 0); err != nil {
		return err
	}
	if created {
		fmt.Printf("已创建用户 #%d（%s）并绑定 admin\n", userID, acc)
	} else {
		fmt.Printf("用户已存在 #%d（%s），已确保 admin 绑定\n", userID, acc)
	}
	fmt.Println("提示：若密码是放在命令行里的，请手动清理 shell history")
	return nil
}

// withAccountService 连库并构造账号服务（只读命令与写命令共用）。
func withAccountService(fn func(ctx context.Context, svc *ucaccount.Service) error) error {
	ctx := context.Background()
	cfg, err := config.Load(0)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer pool.Close()
	return fn(ctx, ucaccount.NewService(postgres.NewAccountRepo(pool)))
}

// parseRoleFlag 解析 --role 参数。
func parseRoleFlag(code string) (daccount.Role, error) {
	role, ok := daccount.ParseRole(strings.TrimSpace(code))
	if !ok {
		return "", fmt.Errorf("角色非法: %q（允许 admin|pm|dev|qa|guest）", code)
	}
	return role, nil
}

// roleList 把角色集合渲染成 "pm, qa"。
func roleList(roles []daccount.Role) string {
	parts := make([]string, 0, len(roles))
	for _, r := range roles {
		parts = append(parts, string(r))
	}
	return strings.Join(parts, ", ")
}

// printPermissionTable 打印某用户的有效权限（只显示允许项，拒绝项不刷屏）。
func printPermissionTable(perms []daccount.Permission) {
	byRes := map[daccount.Resource][]string{}
	for _, p := range perms {
		if p.Allowed {
			byRes[p.Resource] = append(byRes[p.Resource], p.Verb)
		}
	}
	fmt.Println("有效权限（并集）：")
	for _, res := range daccount.AllResources() {
		acts := byRes[res]
		if len(acts) == 0 {
			continue
		}
		fmt.Printf("  %-9s %s\n", res, strings.Join(acts, ", "))
	}
}
