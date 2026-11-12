package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv 从文件加载 KEY=VALUE 到进程环境变量（development 便利）。
// 已存在的环境变量不被覆盖（真实环境优先）；文件不存在静默跳过。
// 凭据永不入库：.env 已在 .gitignore 中（AGENTS.md §4）。
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
			if k != "" && os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
	}
	return sc.Err()
}
