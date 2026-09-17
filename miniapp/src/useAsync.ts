import { useCallback, useEffect, useState } from "react";
import { ApiError } from "./api";

export interface AsyncState<T> {
  data: T | undefined;
  error: ApiError | undefined;
  loading: boolean;
  /** Re-runs the loader; the current data stays on screen while it does. */
  reload: () => void;
  /** Replaces the data locally, for optimistic updates. */
  set: (updater: T | ((current: T) => T)) => void;
}

/**
 * Loads data and keeps it. Deliberately minimal: there is no cache and no
 * request dedupe, because every screen here loads one thing and a trip has a
 * handful of records, not thousands.
 */
export function useAsync<T>(loader: () => Promise<T>, deps: unknown[] = []): AsyncState<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<ApiError | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [nonce, setNonce] = useState(0);

  // The loader is recreated on every render by design; the caller's deps say
  // when it actually changes.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const run = useCallback(loader, deps);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    run()
      .then((result) => {
        if (cancelled) return;
        setData(result);
        setError(undefined);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(
          err instanceof ApiError ? err : new ApiError(0, "unknown", "Something went wrong."),
        );
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [run, nonce]);

  const set = useCallback((updater: T | ((current: T) => T)) => {
    setData((current) =>
      typeof updater === "function"
        ? current === undefined
          ? current
          : (updater as (c: T) => T)(current)
        : updater,
    );
  }, []);

  return {
    data,
    error,
    loading,
    reload: useCallback(() => setNonce((n) => n + 1), []),
    set,
  };
}
