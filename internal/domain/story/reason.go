package story

// 关闭原因（对应禅道 module/story/lang/zh-cn.php reasonList，SPEC-STORY-001 附录 B）。
const (
	ReasonDone           = "done"           // 已完成
	ReasonSubdivided     = "subdivided"     // 已拆分
	ReasonDuplicate      = "duplicate"      // 重复需求
	ReasonPostponed      = "postponed"      // 延期
	ReasonWillNotDo      = "willnotdo"      // 不做
	ReasonCancel         = "cancel"         // 取消
	ReasonByDesign       = "bydesign"       // 设计如此
	ReasonReviewRejected = "reviewrejected" // 评审拒绝（系统内部使用，不对外开放）
)

// AllCloseReasons 返回对用户开放的关闭原因（7 种，不含系统内部 reason）。
func AllCloseReasons() []string {
	return []string{
		ReasonDone, ReasonSubdivided, ReasonDuplicate, ReasonPostponed,
		ReasonWillNotDo, ReasonCancel, ReasonByDesign,
	}
}

// ValidCloseReason 报告原因是否合法（含系统内部 reason）。
func ValidCloseReason(reason string) bool {
	switch reason {
	case ReasonDone, ReasonSubdivided, ReasonDuplicate, ReasonPostponed,
		ReasonWillNotDo, ReasonCancel, ReasonByDesign, ReasonReviewRejected:
		return true
	}
	return false
}

// isPublicCloseReason 报告原因是否为对用户开放的关闭原因。
// close 动作只接受公开的 7 种；reviewrejected 只能由 reviewReject 写入（SPEC-STORY-001 附录 B）。
func isPublicCloseReason(reason string) bool {
	for _, r := range AllCloseReasons() {
		if r == reason {
			return true
		}
	}
	return false
}
