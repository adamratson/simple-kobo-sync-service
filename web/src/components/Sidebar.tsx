import { useState } from "react";
import type { ActivityEvent, Book, Status } from "../api";
import { deviceFromUserAgent, fullDate, timeAgo } from "../format";

export function Setup({ status }: { status: Status }) {
  const [copied, setCopied] = useState(false);
  const line = `api_endpoint=${status.apiEndpoint}`;

  async function copy() {
    try {
      await navigator.clipboard.writeText(line);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API needs a secure context; plain-http LAN pages won't have it.
      window.prompt("Copy this line:", line);
    }
  }

  return (
    <section className="card">
      <h2>Device setup</h2>
      <p className="muted small">
        In <code>.kobo/Kobo/Kobo eReader.conf</code>, under <code>[OneStoreServices]</code>:
      </p>
      <div className="codeline">
        <code>{line}</code>
        <button className="btn small" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <dl className="facts compact">
        {status.lastSync && (
          <>
            <dt>Last device</dt>
            <dd title={status.lastSync.userAgent}>{deviceFromUserAgent(status.lastSync.userAgent)}</dd>
          </>
        )}
        <dt>KEPUB conversion</dt>
        <dd>{status.kepubify ? "On" : "Off"}</dd>
        <dt>Hardcover metadata</dt>
        <dd>{status.hardcover ? "On" : "Off"}</dd>
        <dt>Library folder</dt>
        <dd className="mono small" title={status.epubDir}>
          {status.epubDir}
          {!status.epubDirWritable && <span className="muted"> (read-only)</span>}
        </dd>
      </dl>
    </section>
  );
}

const verbs: Record<ActivityEvent["kind"], string> = {
  sync: "Kobo synced",
  download: "Downloaded to Kobo",
  status: "",
  upload: "Uploaded",
  delete: "Deleted",
};

function describe(e: ActivityEvent, titles: Map<string, string>): { what: string; title?: string } {
  const title = (e.bookId && titles.get(e.bookId)) || e.title || undefined;
  switch (e.kind) {
    case "sync":
      return { what: `Kobo synced · ${e.detail ?? ""}` };
    case "status":
      return { what: e.detail === "Finished" ? "Finished reading" : "Started reading", title };
    default:
      return { what: verbs[e.kind] ?? e.kind, title };
  }
}

export function Activity({ events, books, now }: { events: ActivityEvent[]; books: Book[]; now: number }) {
  const titles = new Map(books.map((b) => [b.id, b.title]));
  return (
    <section className="card">
      <h2>Activity</h2>
      {events.length === 0 ? (
        <p className="muted small">Nothing yet. Press Sync on your Kobo to get started.</p>
      ) : (
        <ol className="activity">
          {events.slice(0, 25).map((e, i) => {
            const d = describe(e, titles);
            return (
              <li key={`${e.at}-${i}`} className={`ev ev-${e.kind}`}>
                <span className="ev-dot" aria-hidden />
                <div>
                  <div>
                    {d.what}
                    {d.title && <span className="ev-title"> {d.title}</span>}
                  </div>
                  <time className="muted small" dateTime={e.at} title={fullDate(e.at)}>
                    {timeAgo(e.at, now)}
                  </time>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
