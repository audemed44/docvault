export type Tone = "good" | "warn" | "bad" | "accent" | "";

export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const s = Math.max(0, (now - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 48 * 3600) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "2024-03-15" → "15 Mar 2024". */
export function formatDate(d: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(d);
  if (!m) return d;
  return `${Number(m[3])} ${MONTHS[Number(m[2]) - 1]} ${m[1]}`;
}

export function today(now = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`;
}

/** Whole days from today to a YYYY-MM-DD date (negative: past). */
export function daysUntil(d: string, now = new Date()): number {
  const [y, m, day] = d.split("-").map(Number);
  const target = Date.UTC(y, m - 1, day);
  const base = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
  return Math.round((target - base) / 86_400_000);
}

export function expiryText(d: string, now = new Date()): { text: string; tone: Tone } {
  const n = daysUntil(d, now);
  if (n < 0) return { text: `Expired ${formatDate(d)}`, tone: "bad" };
  if (n === 0) return { text: "Expires today", tone: "bad" };
  if (n <= 60) return { text: `Expires in ${plural(n, "day")}`, tone: "warn" };
  return { text: `Expires ${formatDate(d)}`, tone: "" };
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

/** Splits a search snippet into plain and matched parts. */
export function snippetParts(s: string): { text: string; match: boolean }[] {
  const out: { text: string; match: boolean }[] = [];
  const re = /\u0002([^\u0003]*)\u0003/g;
  let last = 0;
  for (let m = re.exec(s); m; m = re.exec(s)) {
    if (m.index > last) out.push({ text: s.slice(last, m.index), match: false });
    out.push({ text: m[1], match: true });
    last = m.index + m[0].length;
  }
  if (last < s.length) out.push({ text: s.slice(last), match: false });
  return out.map((p) => ({ ...p, text: p.text.replace(/\s+/g, " ") }));
}

/** "eng+hin" → "English + Hindi". */
const LANGS: Record<string, string> = { eng: "English", hin: "Hindi" };
export function langName(spec: string): string {
  return spec
    .split("+")
    .map((l) => LANGS[l] ?? l)
    .join(" + ");
}
