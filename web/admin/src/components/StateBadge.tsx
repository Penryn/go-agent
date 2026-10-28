interface StateBadgeProps {
  value: string;
  label?: string;
}

function tone(value: string): "positive" | "warning" | "danger" | "neutral" {
  switch (value) {
    case "active":
    case "ready":
    case "sent":
    case "complete":
    case "completed":
      return "positive";
    case "blocked":
    case "forgotten":
    case "skipped":
    case "pending":
      return "warning";
    case "error":
    case "failed":
    case "rejected":
      return "danger";
    default:
      return "neutral";
  }
}

export function StateBadge({ value, label }: StateBadgeProps) {
  return <span className={`state-badge state-${tone(value)}`}>{label ?? value}</span>;
}
