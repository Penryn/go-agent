import type { ReactNode } from "react";

interface ViewHeaderProps {
  eyebrow: string;
  title: string;
  description: string;
  count?: number;
  action?: ReactNode;
}

export function ViewHeader({ eyebrow, title, description, count, action }: ViewHeaderProps) {
  return (
    <header className="view-header">
      <div>
        <p className="eyebrow">{eyebrow}</p>
        <div className="view-title-row">
          <h2>{title}</h2>
          {count !== undefined ? <span className="count-pill">{count}</span> : null}
        </div>
        <p className="view-description">{description}</p>
      </div>
      {action ? <div className="view-action">{action}</div> : null}
    </header>
  );
}
