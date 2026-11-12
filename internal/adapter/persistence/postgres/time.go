// Package postgres 是 Regto 的 PostgreSQL 持久层适配器。
// 依赖方向：adapter → domain（领域层不反向依赖本包，见 AGENTS.md §2）。
package postgres

import (
	"time"
)

// ms 把 unix 毫秒转为 UTC 时间；0 视为零值返回 time.Time{}（供 NULL 判定）。
func ms(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// msOf 把数据库时间转为 unix 毫秒；NULL/零值返回 0。
func msOf(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// msPtr 把可取空的时间指针转为 *int64；nil 或零值返回 nil。
func msPtr(t *time.Time) *int64 {
	if t == nil || t.IsZero() {
		return nil
	}
	v := t.UnixMilli()
	return &v
}

// timeOrNil 把毫秒指针转为数据库时间参数；nil 或 0 返回 nil（写 NULL）。
func timeOrNil(v *int64) *time.Time {
	if v == nil || *v == 0 {
		return nil
	}
	t := time.UnixMilli(*v).UTC()
	return &t
}
