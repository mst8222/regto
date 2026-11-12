// Package migrations 以 embed.FS 提供 SQL 迁移文件，编译进二进制（ROADMAP §3.3 迁移规范）。
package migrations

import "embed"

// FS 是 migrations/ 目录下全部 SQL 文件的只读视图。
// 新增迁移只需放 .up.sql / .down.sql 文件，无需改代码。
//
//go:embed *.sql
var FS embed.FS
