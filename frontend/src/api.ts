import type {
  Category,
  Count,
  Doc,
  DocList,
  Facets,
  Filter,
  ImportReport,
  Session,
  Settings,
  UploadResponse,
  User,
  VocabTag,
} from "./types";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

/** Called when the session has expired, so the app can show the sign-in. */
let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: "same-origin", ...init });
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      message = (await res.json()).error ?? message;
    } catch {
      // not JSON
    }
    if (res.status === 401 && !path.startsWith("/api/session") && !path.startsWith("/api/setup")) {
      onUnauthorized();
    }
    throw new ApiError(message, res.status);
  }
  if (res.status === 204) return undefined as T;
  const type = res.headers.get("Content-Type") ?? "";
  return (type.includes("json") ? res.json() : res.text()) as Promise<T>;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export function filterQuery(f: Partial<Filter>, extra: Record<string, string> = {}): string {
  const p = new URLSearchParams();
  if (f.q?.trim()) p.set("q", f.q.trim());
  if (f.space) p.set("space", f.space);
  if (f.category) p.set("category", f.category);
  if (f.tag) p.set("tag", f.tag);
  if (f.year) p.set("year", f.year);
  if (f.expiring) p.set("expiring", "1");
  if (f.status) p.set("status", f.status);
  if (f.suggested) p.set("suggested", "1");
  if (f.unclassified) p.set("unclassified", "1");
  for (const [k, v] of Object.entries(extra)) p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : "";
}

export const docURL = {
  file: (id: number, download = false) =>
    `/api/documents/${id}/file${download ? "?download=1" : ""}`,
  original: (id: number) => `/api/documents/${id}/original`,
  thumb: (d: Doc) => `/api/documents/${d.id}/thumb?v=${encodeURIComponent(d.updated + d.status)}`,
};

export interface UploadFields {
  title?: string;
  category?: string;
  tags?: string;
  date?: string;
  notes?: string;
  space?: "family" | "private";
  /** The file's own time (ms), for imports. */
  modified?: number;
}

/** Uploads with progress (fetch has no upload progress). */
export function upload(
  files: File[],
  fields: UploadFields,
  onProgress?: (fraction: number) => void,
): Promise<UploadResponse> {
  const form = new FormData();
  for (const [k, v] of Object.entries(fields)) {
    if (v !== undefined && v !== "") form.append(k, String(v));
  }
  for (const f of files) form.append("file", f, f.name);
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/upload");
    xhr.setRequestHeader("Accept", "application/json");
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    xhr.onerror = () => reject(new ApiError("network error", 0));
    xhr.onload = () => {
      let body: (UploadResponse & { error?: string }) | null = null;
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        // not JSON
      }
      if (xhr.status === 401) onUnauthorized();
      if (body?.results) resolve(body);
      else reject(new ApiError(body?.error ?? `HTTP ${xhr.status}`, xhr.status));
    };
    xhr.send(form);
  });
}

export const api = {
  session: () => request<Session>("/api/session"),
  login: (username: string) => request<Session>("/api/session", json("POST", { username })),
  setup: (body: { username: string; name: string }) =>
    request<Session>("/api/setup", json("POST", body)),
  logout: () => request<void>("/api/session", { method: "DELETE" }),
  updateMe: (body: { name: string }) => request<User>("/api/me", json("PUT", body)),

  users: () => request<User[]>("/api/users"),
  saveUser: (u: Partial<User>) =>
    u.id
      ? request<User>(`/api/users/${u.id}`, json("PUT", u))
      : request<User>("/api/users", json("POST", u)),
  deleteUser: (id: number) => request<void>(`/api/users/${id}`, { method: "DELETE" }),

  categories: () => request<Category[]>("/api/categories"),
  saveCategories: (cats: Partial<Category>[]) =>
    request<Category[]>("/api/categories", json("PUT", cats)),
  settings: () => request<Settings>("/api/settings"),
  saveSettings: (s: Omit<Settings, "languages" | "classifier">) =>
    request<Settings>("/api/settings", json("PUT", s)),
  tagVocab: () => request<VocabTag[]>("/api/tags"),
  saveTagVocab: (tags: VocabTag[]) => request<VocabTag[]>("/api/tags", json("PUT", tags)),
  unlistedTags: () => request<Count[]>("/api/tags/unlisted"),
  mask: (text: string) => request<{ text: string }>("/api/mask", json("POST", { text })),

  facets: () => request<Facets>("/api/facets"),
  documents: (f: Partial<Filter>, offset = 0, limit = 60) =>
    request<DocList>(
      `/api/documents${filterQuery(f, { offset: String(offset), limit: String(limit) })}`,
    ),
  document: (id: number) => request<Doc>(`/api/documents/${id}`),
  documentText: (id: number) => request<{ text: string }>(`/api/documents/${id}/text`),
  updateDocument: (d: Doc) => request<Doc>(`/api/documents/${d.id}`, json("PUT", d)),
  deleteDocument: (id: number) => request<void>(`/api/documents/${id}`, { method: "DELETE" }),
  reprocess: (id: number, ocr: boolean, lang = "") =>
    request<Doc>(`/api/documents/${id}/reprocess`, json("POST", { ocr, lang })),
  applySuggestion: (id: number) =>
    request<Doc>(`/api/documents/${id}/suggestion`, { method: "POST" }),
  dismissSuggestion: (id: number) =>
    request<void>(`/api/documents/${id}/suggestion`, { method: "DELETE" }),
  classifierInput: (id: number) =>
    request<{ text: string }>(`/api/documents/${id}/classifier-input`),
  /** Applies the suggestions of the given documents, or of all matching the filter. */
  applySuggestions: (f: Partial<Filter>, ids?: number[]) =>
    request<{ applied: number }>(
      `/api/suggestions/apply${filterQuery(f)}`,
      ids ? json("POST", { ids }) : { method: "POST" },
    ),
  /** Asks for suggestions for the given documents, or all matching the filter. */
  requestSuggestions: (f: Partial<Filter>, ids?: number[]) =>
    request<{ queued: number }>(
      `/api/suggestions/request${filterQuery(f)}`,
      ids ? json("POST", { ids }) : { method: "POST" },
    ),

  importStatus: () => request<ImportReport>("/api/import"),
  startImport: () => request<ImportReport>("/api/import", { method: "POST" }),
};
