import type { ReactNode } from "react";
import type { AsyncState } from "../useAsync";
import { ErrorState, Skeleton } from "./ui";

/**
 * The frame every screen shares: a title, and one of loading / error / content.
 *
 * Centralising the three states is what keeps them consistent — and stops a
 * screen from silently rendering an empty list while a request is still in
 * flight.
 */
export function Screen({
  title,
  subtitle,
  action,
  children,
}: {
  title: string;
  subtitle?: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="screen">
      <div className="screen-header">
        <div>
          <h1 className="screen-title">{title}</h1>
          {subtitle && <p className="screen-subtitle">{subtitle}</p>}
        </div>
        {action}
      </div>
      {children}
    </div>
  );
}

/** Renders the right thing for the state of a loaded resource. */
export function Loaded<T>({
  state,
  children,
  skeletonRows,
}: {
  state: AsyncState<T>;
  children: (data: T) => ReactNode;
  skeletonRows?: number;
}) {
  if (state.error && state.data === undefined) {
    return <ErrorState error={state.error} onRetry={state.reload} />;
  }
  if (state.data === undefined) return <Skeleton rows={skeletonRows} />;
  return <>{children(state.data)}</>;
}
