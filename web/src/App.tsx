import { useState } from "react";
import { BookDialog } from "./components/BookDialog";
import { Library } from "./components/Library";
import { Activity, Setup } from "./components/Sidebar";
import { Upload } from "./components/Upload";
import { bytes, fullDate, timeAgo } from "./format";
import { useLibrary, useNow } from "./useLibrary";

export default function App() {
  const { status, books, events, error, refresh } = useLibrary();
  const now = useNow();
  // Tracked by id so the open dialog picks up fresh data from polling.
  const [openId, setOpenId] = useState<string | null>(null);
  const open = books?.find((b) => b.id === openId) ?? null;

  if (!status || !books) {
    return (
      <div className="app">
        <Header />
        <div className="loading">{error ? <p className="error-text">Can't reach the server: {error}</p> : <p className="muted">Loading…</p>}</div>
      </div>
    );
  }

  const onDevice = books.filter((b) => b.lastDownloaded).length;
  const waiting = books.length - onDevice;
  const last = status.lastSync;

  return (
    <div className="app">
      <Header />
      {error && <div className="banner">Lost connection to the server ({error}). Retrying…</div>}

      <div className="stats">
        <Stat label="Last sync" value={last ? timeAgo(last.at, now) : "Never"} hint={last ? fullDate(last.at) : "Press Sync on your Kobo"} accent={!last ? "warn" : undefined} />
        <Stat label="Books" value={String(books.length)} hint={bytes(status.totalSize)} />
        <Stat label="On device" value={String(onDevice)} hint="downloaded by the Kobo" />
        <Stat label="Waiting for sync" value={String(waiting)} hint={waiting ? "sync your Kobo to fetch" : "all caught up"} accent={waiting ? "warn" : "ok"} />
      </div>

      <div className="layout">
        <main>
          <Upload disabled={!status.epubDirWritable} onUploaded={refresh} />
          <Library books={books} now={now} onOpen={(b) => setOpenId(b.id)} />
        </main>
        <aside>
          <Activity events={events} books={books} now={now} />
          <Setup status={status} />
        </aside>
      </div>

      {open && (
        <BookDialog
          key={open.id}
          book={open}
          now={now}
          canDelete={status.epubDirWritable}
          onClose={() => setOpenId(null)}
          onDeleted={() => {
            setOpenId(null);
            refresh();
          }}
        />
      )}
    </div>
  );
}

function Header() {
  return (
    <header className="header">
      <svg viewBox="0 0 32 32" width="28" height="28" aria-hidden>
        <rect x="6" y="3" width="20" height="26" rx="3" fill="var(--accent)" />
        <rect x="9" y="6" width="14" height="17" rx="1" fill="var(--surface)" />
        <circle cx="16" cy="26" r="1.2" fill="var(--surface)" />
      </svg>
      <h1>Kobo Sync</h1>
    </header>
  );
}

function Stat({ label, value, hint, accent }: { label: string; value: string; hint: string; accent?: "ok" | "warn" }) {
  return (
    <div className={`stat${accent ? ` stat-${accent}` : ""}`}>
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
      <div className="stat-hint" title={hint}>
        {hint}
      </div>
    </div>
  );
}
