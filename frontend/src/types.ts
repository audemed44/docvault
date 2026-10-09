export interface User {
  id: number;
  username: string;
  name: string;
  admin: boolean;
  /** Scales the whole interface. */
  text_size: TextSize;
  created: string;
  /** Private documents (in the People list only). */
  documents: number;
}

export type TextSize = "" | "large" | "larger";

export interface Session {
  authenticated: boolean;
  /** No accounts yet: the first one (an admin) is made on the sign-in page. */
  setup_needed?: boolean;
  user?: User;
  /** Foyer, the homelab's start page (HOMEPAGE_URL). */
  foyer_url?: string;
}

export type Status = "pending" | "processing" | "ready" | "failed";

export interface Suggestion {
  category?: string;
  tags?: string[];
  /** Proposed tags that aren't on the tag list. */
  new_tags?: string[];
  doc_date?: string;
  expires?: string;
}

export interface Doc {
  id: number;
  family: boolean;
  owner_id?: number;
  added_by: string;
  title: string;
  /** 0: uncategorised (the inbox). */
  category_id: number;
  category: string;
  doc_date: string;
  expires: string;
  notes: string;
  tags: string[];
  file_name: string;
  mime: string;
  size: number;
  sha256: string;
  pages: number;
  status: Status;
  error?: string;
  /** Read, but not all of it (pages OCR couldn't read). */
  warning?: string;
  text_source: "" | "pdf" | "ocr";
  ocr_lang?: string;
  suggestion?: Suggestion;
  /** When the classifier last looked at it. */
  classified?: string;
  classify_error?: string;
  created: string;
  updated: string;
  /** Matching text, matches between \u0002 and \u0003 (search only). */
  snippet?: string;
}

export interface DocList {
  documents: Doc[];
  total: number;
}

export interface Category {
  id: number;
  name: string;
  count: number;
}

export interface Count {
  name: string;
  count: number;
}

export interface Facets {
  total: number;
  mine: number;
  family: number;
  inbox: number;
  expiring: number;
  processing: number;
  failed: number;
  /** Read, but not all of it. */
  warnings: number;
  /** With a suggestion waiting. */
  suggested: number;
  /** Processed, but never seen by the classifier. */
  unclassified: number;
  categories: Category[];
  tags: Count[];
  /** Tags that are family members, apart from tags. */
  people: Count[];
  years: Count[];
}

export interface Filter {
  q: string;
  space: "" | "mine" | "family";
  /** "" any, "none" the inbox, or a category ID. */
  category: string;
  tag: string;
  /** A family member's tag. */
  person: string;
  year: string;
  expiring: boolean;
  status: string;
  suggested: boolean;
  unclassified: boolean;
  warnings: boolean;
}

export interface Person {
  /** Also their tag. */
  name: string;
  aliases: string[];
}

export interface Settings {
  ocr_langs: string;
  shortcut_url: string;
  people: Person[];
  mask_words: string[];
  /** What the classifier gets: "auto" (the name, or the text too when the name says nothing), "title" or "text". */
  classify_from: "auto" | "title" | "text";
  /** "ask": suggestions only when asked for; "auto": every document once it's read. */
  suggest_new: "ask" | "auto";
  /** Documents read (OCR'd) at once, 1–8. */
  ocr_workers: number;
  /** Suggestions asked for at once, 1–32. */
  suggest_workers: number;
  languages: string[];
  /** "llm:<model>", "hook", or "" when suggestions are off. */
  classifier: string;
}

export interface VocabTag {
  id?: number;
  name: string;
  /** 0: any category. */
  category_id: number;
}

export type IngestStatus = "added" | "duplicate" | "skipped" | "failed";

export interface IngestResult {
  name: string;
  status: IngestStatus;
  message?: string;
  document?: Doc;
}

export interface UploadResponse {
  message: string;
  results: IngestResult[];
}

export interface ImportReport {
  running: boolean;
  started?: string;
  finished?: string;
  total: number;
  added: number;
  duplicates: number;
  failed: IngestResult[];
  folder: string;
  waiting: number;
}
