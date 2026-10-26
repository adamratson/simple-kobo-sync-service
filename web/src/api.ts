// Types mirror the Go handlers in api.go and activity.go.

export interface SyncRecord {
  at: string;
  userAgent: string;
  books: number;
}

export interface Status {
  apiEndpoint: string;
  epubDir: string;
  epubDirWritable: boolean;
  kepubify: boolean;
  hardcover: boolean;
  bookCount: number;
  totalSize: number;
  lastSync: SyncRecord | null;
}

export type ReadingStatus = "ReadyToRead" | "Reading" | "Finished" | string;

export interface ReadingProgress {
  percent: number;
  status: ReadingStatus;
  spentReadingMinutes: number;
  remainingTimeMinutes: number;
  updatedAt: string;
}

export type MetadataStatus = "disabled" | "pending" | "matched" | "unmatched";

export interface Book {
  id: string;
  title: string;
  author: string;
  language: string;
  fileName: string;
  size: number;
  addedAt: string;
  isbn?: string;
  description?: string;
  series?: string;
  seriesNumber?: number;
  publisher?: string;
  publicationDate?: string;
  genres?: string[];
  hasCover: boolean;
  metadata: MetadataStatus;
  kepubReady: boolean;
  lastDownloaded?: string;
  progress?: ReadingProgress;
}

export type EventKind = "sync" | "download" | "status" | "upload" | "delete";

export interface ActivityEvent {
  at: string;
  kind: EventKind;
  bookId?: string;
  title?: string;
  detail?: string;
}

export interface UploadResult {
  fileName: string;
  book?: Book;
  error?: string;
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      // non-JSON error body; keep the status line
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const api = {
  status: () => request<Status>("/api/status"),
  books: () => request<Book[]>("/api/books"),
  activity: () => request<ActivityEvent[]>("/api/activity"),
  deleteBook: (id: string) => request<void>(`/api/books/${id}`, { method: "DELETE" }),
  coverUrl: (id: string) => `/api/books/${id}/cover`,
  fileUrl: (id: string) => `/api/books/${id}/file`,

  // XHR rather than fetch so the caller gets upload progress events.
  upload(file: File, onProgress: (fraction: number) => void): Promise<UploadResult> {
    return new Promise((resolve, reject) => {
      const form = new FormData();
      form.append("files", file);
      const xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/books");
      xhr.responseType = "json";
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded / e.total);
      };
      xhr.onload = () => {
        const body = xhr.response;
        if (xhr.status === 200 && Array.isArray(body) && body.length > 0) {
          resolve(body[0] as UploadResult);
        } else {
          reject(new Error(body?.error ?? `upload failed (${xhr.status})`));
        }
      };
      xhr.onerror = () => reject(new Error("network error"));
      xhr.send(form);
    });
  },
};
