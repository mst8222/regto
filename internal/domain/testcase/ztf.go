package testcase

import (
	"strconv"
	"strings"
)

// ZTF 规范形式（DESIGN-CASE-001 DEC-05）：
//
//	>>> 用例标题 @产品ID @模块ID @类型 @优先级
//	前提：……
//	*** 步骤描述
//	>> 期望结果
//	>> 期望结果
//	** 分组步骤描述          （两个星号 = group 类型，无期望）
//	*** 下一个步骤
//	>> 期望结果
//
// 规则：
//   - 空行分隔用例块；
//   - 块首行必须是以 ">>> " 开头的标题行，其后可跟 0-4 个 @属性（位置参数，
//     从右侧省略：产品/模块/类型/优先级）；
//   - 标题行之后、第一个步骤行之前的非空行整体作为 precondition；
//   - "*** " 开始一个普通步骤，"** " 开始一个分组步骤；
//   - 步骤行之后第一个非 ">>" 行是步骤描述，后续 ">> " 行是检查点（期望结果）；
//   - 导出只依赖结构不依赖原文：导入时丢弃排版差异，导出按固定规范重排，
//     因此 export(parse(export(c))) == export(c) 必然成立（AC-01）。
//
// 编码固定 UTF-8，换行固定 "\n"，文件以单个换行结束。

const (
	ztfCasePrefix  = ">>>"
	ztfStepPrefix  = "***"
	ztfGroupPrefix = "**"
	ztfCheckPrefix = ">>"
)

// ParseZTF 解析 ZTF 文本为用例列表（纯函数，REQ-05）。
// 语法错误返回 ErrInvalidZTF（Meta 带 line，AC-03）。
// productID 为 0 表示用例块未带 @产品 属性（由调用方决定默认归属）。
func ParseZTF(text string) ([]*Case, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var (
		cases   []*Case
		cur     *Case
		curStep *Step
		// precond 累积当前用例的前提行
		precond []string
		// inSteps 标记是否已进入步骤区（此后非 >> 行只能是步骤描述）
		inSteps bool
	)

	flush := func() {
		if cur == nil {
			return
		}
		cur.Precondition = strings.Join(precond, "\n")
		// 未闭合的步骤也要提交（描述已读、无检查点合法）
		cases = append(cases, cur)
		cur, curStep, precond, inSteps = nil, nil, nil, false
	}

	for i, raw := range lines {
		lineNo := i + 1
		// 整行 TrimSpace：导入即抹平排版差异（行首缩进、行尾空白、CRLF），
		// 这是 round-trip 无损的前提——导出永远按规范形式重排。
		line := strings.TrimSpace(raw)
		if line == "" {
			flush()
			continue
		}

		switch {
		// 顺序不可换：">>>" 必须先于 ">>" 匹配，"***" 必须先于 "**" 匹配。
		case strings.HasPrefix(line, ztfCasePrefix):
			flush()
			c, err := parseCaseHeader(line, lineNo)
			if err != nil {
				return nil, err
			}
			cur = c
			curStep, precond, inSteps = nil, nil, false

		case cur == nil:
			return nil, NewZTFError(lineNo, "用例块必须以 '>>> 标题' 开头")

		case strings.HasPrefix(line, ztfStepPrefix):
			desc := strings.TrimSpace(strings.TrimPrefix(line, ztfStepPrefix))
			if desc == "" {
				return nil, NewZTFError(lineNo, "步骤描述不能为空")
			}
			cur.Steps = append(cur.Steps, Step{
				Seq:      len(cur.Steps) + 1,
				Name:     desc,
				StepType: StepTypeStep,
			})
			curStep = &cur.Steps[len(cur.Steps)-1]
			inSteps = true

		case strings.HasPrefix(line, ztfGroupPrefix):
			desc := strings.TrimSpace(strings.TrimPrefix(line, ztfGroupPrefix))
			if desc == "" {
				return nil, NewZTFError(lineNo, "分组步骤描述不能为空")
			}
			cur.Steps = append(cur.Steps, Step{
				Seq:      len(cur.Steps) + 1,
				Name:     desc,
				StepType: StepTypeGroup,
			})
			curStep = &cur.Steps[len(cur.Steps)-1]
			inSteps = true

		case strings.HasPrefix(line, ztfCheckPrefix):
			if curStep == nil {
				return nil, NewZTFError(lineNo, "检查点 '>>' 之前必须有步骤")
			}
			expect := strings.TrimSpace(strings.TrimPrefix(line, ztfCheckPrefix))
			if expect == "" {
				return nil, NewZTFError(lineNo, "检查点内容不能为空")
			}
			if curStep.StepType == StepTypeGroup {
				return nil, NewZTFError(lineNo, "分组步骤不能有检查点")
			}
			if curStep.Expect == "" {
				curStep.Expect = expect
			} else {
				// 多个检查点合并为换行分隔的期望（导出时拆回多行 >>）。
				curStep.Expect += "\n" + expect
			}

		case inSteps:
			// 步骤区内的非 >> 行：描述必须写在 *** 行上，此处是笔误，报错指正。
			return nil, NewZTFError(lineNo, "步骤描述应写在 '*** ' 行上；检查点请用 '>> ' 前缀")

		default:
			// 标题行之后、步骤区之前：precondition 累积行
			precond = append(precond, line)
		}
	}
	flush()
	return cases, nil
}

// parseCaseHeader 解析 ">>> 标题 @产品 @模块 @类型 @优先级"。
// **标题 = 首个 @ 属性字段之前的全部文本**（可含空格，REQ-05 标题边界）：
// 先按空白切分字段，找到首个 "@" 前缀字段的位置，之前的字段以单空格
// 重新 join 为标题（如 "用户登录 成功后跳转" 不被截断）。
// @属性是**位置参数**：第 1 个=产品 ID，第 2 个=模块 ID，第 3 个=类型，第 4 个=优先级；
// 从右侧省略（可只写 @1）。@属性之后出现的非 @ 字段视为标题尾巴杂质，忽略。
// 已知边界：标题首字段以 @ 开头会被识别为属性（格式固有歧义，SPEC-CASE-001 REQ-05）。
func parseCaseHeader(line string, lineNo int) (*Case, error) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, ztfCasePrefix))
	if rest == "" {
		return nil, NewZTFError(lineNo, "用例标题不能为空")
	}
	fields := strings.Fields(rest)
	attrStart := len(fields)
	for i, f := range fields {
		if strings.HasPrefix(f, "@") {
			attrStart = i
			break
		}
	}
	title := strings.TrimSpace(strings.Join(fields[:attrStart], " "))
	if title == "" {
		return nil, NewZTFError(lineNo, "用例标题不能为空")
	}
	c := &Case{Title: title, Status: StatusWait, Version: 1, StoryVersion: 1, Type: TypeFeature, Pri: 3}

	pos := 0
	for _, f := range fields[attrStart:] {
		if !strings.HasPrefix(f, "@") {
			break
		}
		v := strings.TrimPrefix(f, "@")
		pos++
		switch pos {
		case 1: // 产品 ID（必须为正，0 视为缺省由调用方补）
			id, ok := parseID(v)
			if !ok || id == 0 {
				return nil, NewZTFError(lineNo, "@产品 必须是正整数: "+v)
			}
			c.ProductID = id
		case 2: // 模块 ID；@0 是「无模块」占位（导出侧对空模块写 0）
			id, ok := parseID(v)
			if !ok {
				return nil, NewZTFError(lineNo, "@模块 必须是非负整数: "+v)
			}
			if id > 0 {
				c.ModuleID = &id
			}
		case 3: // 类型
			if !CaseType(v).Valid() {
				return nil, NewZTFError(lineNo, "@类型 非法: "+v)
			}
			c.Type = CaseType(v)
		case 4: // 优先级 1-4
			p, ok := parseID(v)
			if !ok || p < 1 || p > 4 {
				return nil, NewZTFError(lineNo, "@优先级 必须是 1-4: "+v)
			}
			c.Pri = int(p)
		default:
			return nil, NewZTFError(lineNo, "@属性最多 4 个（产品/模块/类型/优先级）")
		}
	}
	return c, nil
}

// parseID 解析非负整数（拒绝负数与非数字；0 合法，用于「无模块」占位）。
func parseID(s string) (int64, bool) {
	if !isDigits(s) {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 0 {
		return 0, false
	}
	return id, true
}

// ExportZTF 把单个用例导出为 ZTF 规范形式文本（纯函数，REQ-06）。
// 输出以 "\n" 结尾、无多余空行；多检查点拆为多行 ">> "。
func ExportZTF(c *Case) string {
	var b strings.Builder
	b.WriteString(ztfCasePrefix)
	b.WriteString(" ")
	b.WriteString(c.Title)
	// 位置参数规则：一旦写出第 N 位，其左侧所有位必须显式填充（含默认值），
	// 否则解析侧会把值读进错误的位置。默认值填充：模块=@0、类型=@feature。
	writePri := c.Pri != 3
	writeType := c.Type != TypeFeature || writePri
	// 模块槽位：自身有值必须写；自身为空但后续槽位要写，则填 @0 占位。
	writeModule := c.ModuleID != nil || writeType
	writeAttr(&b, c.ProductID > 0, strconv.FormatInt(c.ProductID, 10))
	if writeModule {
		if c.ModuleID != nil {
			writeAttr(&b, true, strconv.FormatInt(*c.ModuleID, 10))
		} else {
			writeAttr(&b, true, "0")
		}
	}
	if writeType {
		writeAttr(&b, true, string(c.Type))
	}
	if writePri {
		writeAttr(&b, true, strconv.Itoa(c.Pri))
	}
	b.WriteString("\n")

	if c.Precondition != "" {
		for _, ln := range strings.Split(c.Precondition, "\n") {
			if ln != "" {
				b.WriteString(ln)
				b.WriteString("\n")
			}
		}
	}
	for _, s := range c.Steps {
		if s.StepType == StepTypeGroup {
			b.WriteString(ztfGroupPrefix)
			b.WriteString(" ")
			b.WriteString(s.Name)
			b.WriteString("\n")
			continue
		}
		b.WriteString(ztfStepPrefix)
		b.WriteString(" ")
		b.WriteString(s.Name)
		b.WriteString("\n")
		for _, cp := range strings.Split(s.Expect, "\n") {
			if cp == "" {
				continue
			}
			b.WriteString(ztfCheckPrefix)
			b.WriteString(" ")
			b.WriteString(cp)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ExportZTFCases 导出多个用例（块间以一个空行分隔）。
func ExportZTFCases(cases []*Case) string {
	blocks := make([]string, 0, len(cases))
	for _, c := range cases {
		blocks = append(blocks, strings.TrimSuffix(ExportZTF(c), "\n"))
	}
	if len(blocks) == 0 {
		return ""
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

// writeAttr 追加 " @值" 属性。
func writeAttr(b *strings.Builder, cond bool, val string) {
	if !cond {
		return
	}
	b.WriteString(" @")
	b.WriteString(val)
}

// isDigits 报告字符串是否为非空纯数字。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
