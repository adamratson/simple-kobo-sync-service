import { useMemo, useState } from "react";
import { api, type Book } from "../api";
import { bytes, fullDate, seriesLabel, timeAgo } from "../format";
import { Cover } from "./Cover";

type Filter = "all" | "waiting" | "device" | "reading" | "finished";
type Sort = "added" | "title" | "author";

const filters: { id: Filter; label: string; test: (b: Book) => boolean }[] = [
  { id: "all", label: "All", test: () => true },
  { id: "waiting", label: "Waiting for sync", test: (b) => !b.lastDownloaded },
  { id: "device", label: "On device", test: (b) => !!b.lastDownloaded },
  { id: "reading", label: "Reading", test: (b) => b.progress?.status === "Reading" },
  { id: "finished", label: "Finished", test: (b) => b.progress?.status === "Finished" },
];

const sorters: Record<Sort, (a: Book, b: Book) => number> = {
  added: (a, b) => b.addedAt.localeCompare(a.addedAt),
  title: (a, b) => a.title.localeCompare(b.title),
  author: (a, b) => a.author.localeCompare(b.author) || a.title.localeCompare(b.title),
};

export function Library({ books, now, onOpen }: { books: Book[]; now: number; onOpen: (b: Book) => void }) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [sort, setSort] = useState<Sort>("added");

  const counts = useMemo(
    () => Object.fromEntries(filters.map((f) => [f.id, books.filter(f.test).length])) as Record<Filter, number>,
    [books],
  );

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    const test = filters.find((f) => f.id === filter)!.test;
    return books
      .filter(test)
      .filter(
        (b) =>
          !q ||
          b.title.toLowerCase().includes(q) ||
          b.author.toLowerCase().includes(q) ||
          (b.series ?? "").toLowerCase().includes(q) ||
          b.fileName.toLowerCase().includes(q),
      )
      .sort(sorters[sort]);
  }, [books, query, filter, sort]);

  return (
    <section className="card library">
      <div className="library-head">
        <h2>Library</h2>
        <div className="library-controls">
          <input
            type="search"
            placeholder="Search title, author, series…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search books"
          />
          <select value={sort} onChange={(e) => setSort(e.target.value as Sort)} aria-label="Sort by">
            <option value="added">Recently added</option>
            <option value="title">Title</option>
            <option value="author">Author</option>
          </select>
        </div>
      </div>

      <div className="chips" role="tablist">
        {filters.map((f) => (
          <button
            key={f.id}
            role="tab"
            aria-selected={filter === f.id}
            className={`chip${filter === f.id ? " active" : ""}`}
            onClick={() => setFilter(f.id)}
            disabled={f.id !== "all" && counts[f.id] === 0}
          >
            {f.label} <span className="chip-count">{counts[f.id]}</span>
          </button>
        ))}
      </div>

      {books.length === 0 ? (
        <div className="empty">
          <p>No books yet.</p>
          <p className="muted">Upload an EPUB above, or drop files into the library folder.</p>
        </div>
      ) : shown.length === 0 ? (
        <div className="empty">
          <p className="muted">No books match.</p>
        </div>
      ) : (
        <ul className="book-list">
          {shown.map((b) => (
            <BookRow key={b.id} book={b} now={now} onOpen={() => onOpen(b)} />
          ))}
        </ul>
      )}
    </section>
  );
}

function BookRow({ book, now, onOpen }: { book: Book; now: number; onOpen: () => void }) {
  const series = seriesLabel(book.series, book.seriesNumber);
  const p = book.progress;
  return (
    <li className="book-row">
      <button className="book-main" onClick={onOpen} aria-label={`Details for ${book.title}`}>
        <Cover book={book} />
        <div className="book-text">
          <div className="book-title">{book.title}</div>
          <div className="book-sub">
            {book.author || <span className="muted">Unknown author</span>}
            {series && <span className="muted"> · {series}</span>}
          </div>
          <div className="book-badges">
            <DeviceBadge book={book} now={now} />
            {p && p.status === "Finished" && <span className="badge done">Finished</span>}
          </div>
          {p && p.status === "Reading" && (
            <div className="progress" title={`${Math.round(p.percent)}% read`}>
              <div className="bar">
                <div style={{ width: `${p.percent}%` }} />
              </div>
              <span>{Math.round(p.percent)}%</span>
            </div>
          )}
        </div>
      </button>
      <div className="book-meta">
        <span title={fullDate(book.addedAt)}>Added {timeAgo(book.addedAt, now)}</span>
        <span>{bytes(book.size)}</span>
      </div>
      <a className="icon-btn" href={api.fileUrl(book.id)} download title="Download EPUB" aria-label={`Download ${book.title}`}>
        <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden>
          <path d="M12 4v12m0 0 4.5-4.5M12 16l-4.5-4.5M4 20h16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </a>
    </li>
  );
}

export function DeviceBadge({ book, now }: { book: Book; now: number }) {
  if (book.lastDownloaded) {
    return (
      <span className="badge ok" title={`Downloaded ${fullDate(book.lastDownloaded)}`}>
        On device · {timeAgo(book.lastDownloaded, now)}
      </span>
    );
  }
  return <span className="badge wait">Waiting for sync</span>;
}
