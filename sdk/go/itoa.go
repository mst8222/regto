package regto

import "strconv"

// itoa 是 int64 → 字符串的小工具（避免每个资源文件都写一遍 strconv）。
func itoa(v int64) string { return strconv.FormatInt(v, 10) }
