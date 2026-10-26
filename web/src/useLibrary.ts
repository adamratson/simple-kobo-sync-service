import { useCallback, useEffect, useRef, useState } from "react";
import { api, type ActivityEvent, type Book, type Status } from "./api";

const POLL_MS = 10_000;

export interface LibraryState {
  status: Status | null;
  books: Book[] | null;
  events: ActivityEvent[];
  error: string | null;
  refresh: () => Promise<void>;
}

// useLibrary loads server state and keeps it fresh: it polls while the tab is
// visible and refetches immediately when the tab regains focus, so "last synced"
// updates shortly after pressing Sync on the device.
export function useLibrary(): LibraryState {
  const [status, setStatus] = useState<Status | null>(null);
  const [books, setBooks] = useState<Book[] | null>(null);
  const [events, setEvents] = useState<ActivityEvent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const inflight = useRef(false);

  const refresh = useCallback(async () => {
    if (inflight.current) return;
    inflight.current = true;
    try {
      const [s, b, e] = await Promise.all([api.status(), api.books(), api.activity()]);
      setStatus(s);
      setBooks(b);
      setEvents(e);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      inflight.current = false;
    }
  }, []);

  useEffect(() => {
    refresh();
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") refresh();
    }, POLL_MS);
    const onVisible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [refresh]);

  return { status, books, events, error, refresh };
}

// useNow re-renders periodically so relative timestamps ("2 minutes ago") stay current.
export function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(t);
  }, [intervalMs]);
  return now;
}
