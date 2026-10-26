import { useEffect, useRef, useState } from "react";
import { api, type Book } from "../api";
import { bytes, fullDate, minutes, seriesLabel } from "../format";
import { Cover } from "./Cover";
import { DeviceBadge } from "./Library";

const metadataLabel: Record<Book["metadata"], string> = {
  disabled: "Hardcover disabled",
  pending: "Fetched on next sync",
  matched: "Matched on Hardcover",
  unmatched: "No Hardcover match",
};

export function BookDialog({
  book,
  now,
  canDelete,
  onClose,
  onDeleted,
}: {
  book: Book;
  now: number;
  canDelete: boolean;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    ref.current?.showModal();
  }, []);

  async function remove() {
    setDeleting(true);
    setError(null);
    try {
      await api.deleteBook(book.id);
      onDeleted();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setDeleting(false);
    }
  }

  const series = seriesLabel(book.series, book.seriesNumber);
  const p = book.progress;

  return (
    <dialog
      ref={ref}
      className="book-dialog"
      onClose={onClose}
      onClick={(e) => {
        // Click on the backdrop (the dialog element itself) closes it.
        if (e.target === ref.current) ref.current?.close();
      }}
    >
      <div className="dialog-body">
        <button className="dialog-close icon-btn" onClick={() => ref.current?.close()} aria-label="Close">
          <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden>
            <path d="M6 6l12 12M18 6 6 18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
          </svg>
        </button>

        <div className="dialog-top">
          <Cover book={book} size="lg" />
          <div>
            <h2>{book.title}</h2>
            <p className="book-sub">{book.author || "Unknown author"}</p>
            {series && <p className="muted">{series}</p>}
            <div className="book-badges">
              <DeviceBadge book={book} now={now} />
            </div>
            {p && p.status !== "ReadyToRead" && (
              <div className="reading">
                <div className="progress">
                  <div className="bar">
                    <div style={{ width: `${p.status === "Finished" ? 100 : p.percent}%` }} />
                  </div>
                  <span>{p.status === "Finished" ? "Finished" : `${Math.round(p.percent)}%`}</span>
                </div>
                <p className="muted small">
                  {p.spentReadingMinutes > 0 && <>{minutes(p.spentReadingMinutes)} read</>}
                  {p.status === "Reading" && p.remainingTimeMinutes > 0 && <> · ~{minutes(p.remainingTimeMinutes)} left</>}
                  {" · "}updated {fullDate(p.updatedAt)}
                </p>
              </div>
            )}
          </div>
        </div>

        {book.description && <p className="description">{book.description}</p>}

        {book.genres && book.genres.length > 0 && (
          <div className="genres">
            {book.genres.map((g) => (
              <span key={g} className="chip static">
                {g}
              </span>
            ))}
          </div>
        )}

        <dl className="facts">
          {book.publisher && (
            <>
              <dt>Publisher</dt>
              <dd>{book.publisher}</dd>
            </>
          )}
          {book.publicationDate && (
            <>
              <dt>Published</dt>
              <dd>{book.publicationDate}</dd>
            </>
          )}
          {book.isbn && (
            <>
              <dt>ISBN</dt>
              <dd>{book.isbn}</dd>
            </>
          )}
          <dt>Language</dt>
          <dd>{book.language}</dd>
          <dt>File</dt>
          <dd className="mono">{book.fileName}</dd>
          <dt>Size</dt>
          <dd>{bytes(book.size)}</dd>
          <dt>Added</dt>
          <dd>{fullDate(book.addedAt)}</dd>
          <dt>Metadata</dt>
          <dd>{metadataLabel[book.metadata]}</dd>
          <dt>KEPUB</dt>
          <dd>{book.kepubReady ? "Converted and cached" : "Converted on first download"}</dd>
          <dt>Book ID</dt>
          <dd className="mono small">{book.id}</dd>
        </dl>

        {error && <p className="error-text">{error}</p>}

        <div className="dialog-actions">
          <a className="btn" href={api.fileUrl(book.id)} download>
            Download EPUB
          </a>
          {canDelete &&
            (confirming ? (
              <div className="confirm">
                <span className="muted small">Removes the file from the server. Copies already on your Kobo stay there.</span>
                <button className="btn" onClick={() => setConfirming(false)} disabled={deleting}>
                  Cancel
                </button>
                <button className="btn danger" onClick={remove} disabled={deleting}>
                  {deleting ? "Deleting…" : "Delete"}
                </button>
              </div>
            ) : (
              <button className="btn danger-ghost" onClick={() => setConfirming(true)}>
                Delete from library
              </button>
            ))}
        </div>
      </div>
    </dialog>
  );
}
