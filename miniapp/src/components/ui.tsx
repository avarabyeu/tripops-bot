/**
 * The small set of building blocks every screen uses.
 *
 * The product asks for clear loading, empty and error states everywhere, so
 * they are components rather than something each screen improvises.
 */

import type { ReactNode } from "react";
import { useEffect, useState } from "react";
import { initials } from "../format";
import { haptic } from "../telegram";
import type { ApiError } from "../api";

export function Card({
  children,
  onClick,
  tight,
}: {
  children: ReactNode;
  onClick?: () => void;
  tight?: boolean;
}) {
  const className = `card${tight ? " tight" : ""}${onClick ? " tappable" : ""}`;
  if (!onClick) return <div className={className}>{children}</div>;
  return (
    <div
      className={className}
      role="button"
      tabIndex={0}
      onClick={() => {
        haptic.tap();
        onClick();
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") onClick();
      }}
    >
      {children}
    </div>
  );
}

export function Button({
  children,
  onClick,
  variant = "primary",
  disabled,
  block,
  small,
  type = "button",
}: {
  children: ReactNode;
  onClick?: () => void;
  variant?: "primary" | "secondary" | "ghost" | "danger";
  disabled?: boolean;
  block?: boolean;
  small?: boolean;
  type?: "button" | "submit";
}) {
  const classes = ["btn"];
  if (variant !== "primary") classes.push(variant);
  if (block) classes.push("block");
  if (small) classes.push("small");
  return (
    <button
      type={type}
      className={classes.join(" ")}
      disabled={disabled}
      onClick={
        onClick &&
        (() => {
          haptic.tap();
          onClick();
        })
      }
    >
      {children}
    </button>
  );
}

export function Chip({
  children,
  active,
  onClick,
}: {
  children: ReactNode;
  active?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      className={`chip${active ? " active" : ""}`}
      onClick={
        onClick &&
        (() => {
          haptic.tap();
          onClick();
        })
      }
    >
      {children}
    </button>
  );
}

export function Avatar({
  name,
  photo,
  small,
}: {
  name: string;
  photo?: string;
  small?: boolean;
}) {
  const className = `avatar${small ? " small" : ""}`;
  if (photo) return <img className={className} src={photo} alt="" loading="lazy" />;
  return <span className={className}>{initials(name)}</span>;
}

export function ProgressBar({ completed, total }: { completed: number; total: number }) {
  const percent = total === 0 ? 100 : Math.round((completed / total) * 100);
  return (
    <div className="progress" role="progressbar" aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}>
      <span style={{ width: `${percent}%` }} />
    </div>
  );
}

/** The loading state: a shape, not a spinner, so the layout does not jump. */
export function Skeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div className="stack" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="skeleton" style={{ height: i === 0 ? 92 : 68 }} />
      ))}
    </div>
  );
}

/**
 * The empty state. Every section has one, and it says what the section is for
 * rather than just reporting that it is empty.
 */
export function Empty({
  emoji,
  title,
  description,
  bullets,
  action,
}: {
  emoji: string;
  title: string;
  description?: string;
  bullets?: string[];
  action?: ReactNode;
}) {
  return (
    <div className="placeholder">
      <span className="emoji">{emoji}</span>
      <h3>{title}</h3>
      {description && <p>{description}</p>}
      {bullets && (
        <ul>
          {bullets.map((b) => (
            <li key={b}>• {b}</li>
          ))}
        </ul>
      )}
      {action}
    </div>
  );
}

export function ErrorState({ error, onRetry }: { error: ApiError; onRetry?: () => void }) {
  return (
    <div className="placeholder">
      <span className="emoji">{error.retryable ? "📡" : "🚫"}</span>
      <h3>{error.retryable ? "Could not load this" : "That did not work"}</h3>
      <p>{error.message}</p>
      {onRetry && error.retryable && (
        <Button variant="secondary" onClick={onRetry}>
          Try again
        </Button>
      )}
    </div>
  );
}

/** A bottom sheet, which is the only modal shape this app uses. */
export function Sheet({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
    };
  }, [onClose]);

  return (
    <div className="sheet-backdrop" onClick={onClose} role="presentation">
      <div
        className="sheet"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="card-row">
          <h2 className="screen-title">{title}</h2>
          <Button variant="ghost" onClick={onClose}>
            Close
          </Button>
        </div>
        {children}
      </div>
    </div>
  );
}

/** A labelled input with room for the field error the API returns. */
export function Field({
  label,
  error,
  children,
}: {
  label: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {error && <span className="field-error">{error}</span>}
    </div>
  );
}

/**
 * A button that shows it is working and surfaces failure in place.
 *
 * Optimistic updates are fine for a checkbox; anything that can be rejected
 * (capacity, permissions) waits for the server, because a row that flips back
 * a second later is worse than a button that takes a moment.
 */
export function AsyncButton({
  children,
  onClick,
  variant = "primary",
  block,
  small,
  disabled,
}: {
  children: ReactNode;
  onClick: () => Promise<unknown>;
  variant?: "primary" | "secondary" | "ghost" | "danger";
  block?: boolean;
  small?: boolean;
  disabled?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Button
      variant={variant}
      block={block}
      small={small}
      disabled={disabled || busy}
      onClick={() => {
        setBusy(true);
        void onClick().finally(() => setBusy(false));
      }}
    >
      {busy ? "…" : children}
    </Button>
  );
}
