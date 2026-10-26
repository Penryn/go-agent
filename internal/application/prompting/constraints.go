package prompting

import (
	"fmt"
	"slices"
	"strings"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// ConstraintSection renders already-loaded facts; prompt construction does no I/O.
func ConstraintSection(constraints []memorydomain.MemoryConstraint) string {
	if len(constraints) == 0 {
		return ""
	}

	// 构建指令
	var sections []string
	sections = append(sections, "", "记忆约束（必须遵守）:")

	// 按类型分组
	preferredNames := make(map[string]string)
	allowMentions := make(map[string]bool)
	allowPokes := make(map[string]bool)
	temporaryConstraints := []string{}

	for _, c := range constraints {
		if !c.IsValid() {
			continue
		}
		switch c.Type {
		case "preferred_name":
			preferredNames[c.SubjectID] = c.Value

		case "allow_mention":
			if c.Value == "false" {
				allowMentions[c.SubjectID] = false
				if c.IsTemporary() {
					temporaryConstraints = append(temporaryConstraints,
						fmt.Sprintf("User%s: 不允许 @（临时，至 %s）", c.SubjectID, c.ValidUntil.Format("15:04")))
				}
			}

		case "allow_poke":
			if c.Value == "false" {
				allowPokes[c.SubjectID] = false
				if c.IsTemporary() {
					temporaryConstraints = append(temporaryConstraints,
						fmt.Sprintf("User%s: 不允许戳一戳（临时，至 %s）", c.SubjectID, c.ValidUntil.Format("15:04")))
				}
			}
		}
	}

	// 1. 称呼约束
	if len(preferredNames) > 0 {
		names := make([]string, 0, len(preferredNames))
		for userID, name := range preferredNames {
			names = append(names, fmt.Sprintf("User%s=%q", userID, name))
		}
		slices.Sort(names)
		sections = append(sections, "- 称呼方式: "+strings.Join(names, "；")+"。必须使用这些称呼，不得使用其他昵称。")
	}

	// 2. @ 约束
	if len(allowMentions) > 0 {
		disallowed := []string{}
		for userID, allow := range allowMentions {
			if !allow {
				disallowed = append(disallowed, fmt.Sprintf("User%s", userID))
			}
		}
		if len(disallowed) > 0 {
			slices.Sort(disallowed)
			sections = append(sections, "- 禁止 @: "+strings.Join(disallowed, "、")+"。如需联系他们，使用私聊或其他方式。")
		}
	}

	// 3. 戳一戳约束
	if len(allowPokes) > 0 {
		disallowed := []string{}
		for userID, allow := range allowPokes {
			if !allow {
				disallowed = append(disallowed, fmt.Sprintf("User%s", userID))
			}
		}
		if len(disallowed) > 0 {
			slices.Sort(disallowed)
			sections = append(sections, "- 禁止戳一戳: "+strings.Join(disallowed, "、")+"。")
		}
	}

	// 4. 临时约束提示
	if len(temporaryConstraints) > 0 {
		slices.Sort(temporaryConstraints)
		sections = append(sections, "- 临时限制: "+strings.Join(temporaryConstraints, "；")+"。")
	}

	return strings.Join(sections, "\n")
}
