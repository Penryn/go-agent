package memory

import "fmt"

// ValidateInteraction applies the current member's sourced preferences to an
// outward action. Expired preferences no longer restrict interaction.
func ValidateInteraction(constraints []MemoryConstraint, kind, targetID string) error {
	for _, constraint := range constraints {
		if constraint.SubjectID != targetID || !constraint.IsValid() || constraint.Value != "false" {
			continue
		}
		switch kind {
		case "at", "mention":
			if constraint.Type == string(PredicateAllowMention) {
				return fmt.Errorf("不允许 @ User%s", targetID)
			}
		case "poke", "nudge":
			if constraint.Type == string(PredicateAllowPoke) {
				return fmt.Errorf("不允许戳一戳 User%s", targetID)
			}
		}
	}
	return nil
}
