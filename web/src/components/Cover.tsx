import { useState, type CSSProperties } from "react";
import { api, type Book } from "../api";

// Deterministic hue per book so placeholders are distinct but stable.
function hue(id: string): number {
  let h = 0;
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

export function Cover({ book, size = "sm" }: { book: Book; size?: "sm" | "lg" }) {
  const [failed, setFailed] = useState(false);
  const cls = `cover cover-${size}`;
  if (book.hasCover && !failed) {
    return (
      <img
        className={cls}
        src={api.coverUrl(book.id)}
        alt=""
        loading="lazy"
        onError={() => setFailed(true)}
      />
    );
  }
  return (
    <div className={`${cls} cover-placeholder`} style={{ "--hue": hue(book.id) } as CSSProperties} aria-hidden>
      <span>{book.title}</span>
    </div>
  );
}
