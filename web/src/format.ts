const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
];

export function timeAgo(iso: string, now = Date.now()): string {
  const secs = (new Date(iso).getTime() - now) / 1000;
  if (Math.abs(secs) < 45) return "just now";
  for (const [unit, size] of units) {
    if (Math.abs(secs) >= size || unit === "minute") {
      return rtf.format(Math.round(secs / size), unit);
    }
  }
  return "just now";
}

export function fullDate(iso: string): string {
  return new Date(iso).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function minutes(m: number): string {
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  const rest = m % 60;
  return rest ? `${h} h ${rest} min` : `${h} h`;
}

// Kobo user agents end with e.g. "(Kobo Touch 0390/4.45.23646)".
export function deviceFromUserAgent(ua: string): string {
  const m = ua.match(/\(Kobo[^/)]*\/([\d.]+)\)/);
  if (m) return `Kobo · firmware ${m[1]}`;
  return ua.length > 60 ? ua.slice(0, 57) + "…" : ua || "Unknown device";
}

export function seriesLabel(series?: string, n?: number): string | null {
  if (!series) return null;
  return n ? `${series} #${n}` : series;
}
