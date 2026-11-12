package zentao

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// normalizeValue 把驱动返回值归一为 pgx 可绑定的稳定类型
// （MySQL driver 返回 []byte / int64 / float64 / time.Time / nil）。
func normalizeValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(x)
	case sql.NullString:
		if x.Valid {
			return x.String
		}
		return nil
	case sql.NullInt64:
		if x.Valid {
			return x.Int64
		}
		return nil
	case sql.NullFloat64:
		if x.Valid {
			return x.Float64
		}
		return nil
	case sql.NullTime:
		if x.Valid {
			return x.Time
		}
		return nil
	case sql.NullBool:
		if x.Valid {
			return x.Bool
		}
		return nil
	case time.Time:
		return x
	default:
		return v
	}
}

// toStr 把值归一为字符串（nil → ""）。
func toStr(v any) string {
	switch x := normalizeValue(v).(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "1"
		}
		return "0"
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// toInt64 把值归一为 int64。
func toInt64(v any) (int64, bool) {
	switch x := normalizeValue(v).(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		return int64(x), true
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

// toFloat 把值归一为 float64。
func toFloat(v any) (float64, bool) {
	switch x := normalizeValue(v).(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

// toTime 把值归一为 time.Time（字符串按 MySQL datetime 格式解析）。
func toTime(v any) (time.Time, bool) {
	switch x := normalizeValue(v).(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{
			"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00",
			"2006-01-02 15:04:05.999999", "2006-01-02",
		} {
			if t, err := time.Parse(layout, x); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// jsonStringArray 把普通字符串包成 JSONB 数组（casestep.expect → checks: ["..."]）。
// 源值本身是合法 JSON 数组时原样透传。
func jsonStringArray(s string) any {
	var probe any
	if json.Unmarshal([]byte(s), &probe) == nil {
		if _, ok := probe.([]any); ok {
			return json.RawMessage(s)
		}
	}
	b, err := json.Marshal([]string{s})
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}
