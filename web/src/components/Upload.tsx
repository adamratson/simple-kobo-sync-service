import { useRef, useState } from "react";
import { api } from "../api";

interface Item {
  key: string;
  name: string;
  progress: number;
  state: "queued" | "uploading" | "done" | "error";
  message?: string;
}

export function Upload({ disabled, onUploaded }: { disabled: boolean; onUploaded: () => void }) {
  const [items, setItems] = useState<Item[]>([]);
  const [dragging, setDragging] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const busy = items.some((i) => i.state === "queued" || i.state === "uploading");

  const update = (key: string, patch: Partial<Item>) =>
    setItems((prev) => prev.map((i) => (i.key === key ? { ...i, ...patch } : i)));

  async function start(files: File[]) {
    if (disabled || files.length === 0) return;
    const queued = files.map((f, n) => ({
      file: f,
      item: { key: `${Date.now()}-${n}-${f.name}`, name: f.name, progress: 0, state: "queued" } as Item,
    }));
    // Finished items from a previous batch are cleared; failures stay until dismissed.
    setItems((prev) => [...prev.filter((i) => i.state === "error"), ...queued.map((q) => q.item)]);

    // One file per request, sequentially: per-file progress and errors, and a
    // large batch doesn't saturate a small home server.
    for (const { file, item } of queued) {
      if (!file.name.toLowerCase().endsWith(".epub")) {
        update(item.key, { state: "error", message: "Not an .epub file" });
        continue;
      }
      update(item.key, { state: "uploading" });
      try {
        const res = await api.upload(file, (p) => update(item.key, { progress: p }));
        if (res.error) {
          update(item.key, { state: "error", message: res.error });
        } else {
          update(item.key, { state: "done", progress: 1, message: res.book?.title });
        }
      } catch (err) {
        update(item.key, { state: "error", message: err instanceof Error ? err.message : String(err) });
      }
    }
    onUploaded();
  }

  return (
    <section className="card">
      <div
        className={`dropzone${dragging ? " dragging" : ""}${disabled ? " disabled" : ""}`}
        onDragOver={(e) => {
          e.preventDefault();
          if (!disabled) setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragging(false);
          start(Array.from(e.dataTransfer.files));
        }}
        onClick={() => !disabled && input.current?.click()}
        role="button"
        tabIndex={disabled ? -1 : 0}
        onKeyDown={(e) => {
          if ((e.key === "Enter" || e.key === " ") && !disabled) input.current?.click();
        }}
        aria-disabled={disabled}
      >
        <svg viewBox="0 0 24 24" width="28" height="28" aria-hidden>
          <path d="M12 16V4m0 0-4.5 4.5M12 4l4.5 4.5M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        {disabled ? (
          <p>
            <strong>Uploads unavailable</strong>
            <span>The library folder is read-only. Mount it writable to add books here.</span>
          </p>
        ) : (
          <p>
            <strong>{busy ? "Uploading…" : "Drop EPUBs here"}</strong>
            <span>or click to choose files. They'll appear on your Kobo after the next sync.</span>
          </p>
        )}
        <input
          ref={input}
          type="file"
          accept=".epub,application/epub+zip"
          multiple
          hidden
          onChange={(e) => {
            start(Array.from(e.target.files ?? []));
            e.target.value = "";
          }}
        />
      </div>

      {items.length > 0 && (
        <ul className="upload-list">
          {items.map((i) => (
            <li key={i.key} className={`upload-item ${i.state}`}>
              <div className="upload-row">
                <span className="upload-name" title={i.name}>
                  {i.name}
                </span>
                <span className="upload-state">
                  {i.state === "queued" && "Queued"}
                  {i.state === "uploading" && `${Math.round(i.progress * 100)}%`}
                  {i.state === "done" && "Added"}
                  {i.state === "error" && (
                    <button
                      className="link"
                      onClick={() => setItems((prev) => prev.filter((x) => x.key !== i.key))}
                    >
                      Dismiss
                    </button>
                  )}
                </span>
              </div>
              {i.state === "uploading" && (
                <div className="bar">
                  <div style={{ width: `${i.progress * 100}%` }} />
                </div>
              )}
              {i.state === "error" && <div className="upload-msg">{i.message}</div>}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
