import {
  Briefcase,
  Car,
  Check,
  ChevronRight,
  FileText,
  Folder,
  GraduationCap,
  HeartPulse,
  House,
  IdCard,
  Inbox,
  Landmark,
  Loader,
  Plane,
  Receipt,
  ReceiptText,
  Shield,
  TriangleAlert,
  Users,
  type LucideIcon,
} from "lucide-preact";
import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { docURL } from "../api";
import { categoryColor, expiryText, formatDate, snippetParts } from "../lib";
import type { Category, Doc, IngestResult } from "../types";

/** Page one of a document, or a stand-in while it's processed. */
export function DocThumb(props: { doc: Doc; class?: string }) {
  const { doc } = props;
  const [broken, setBroken] = useState(false);
  const busy = doc.status === "pending" || doc.status === "processing";
  return (
    <div class={`thumb ${props.class ?? ""}`}>
      {!broken && !busy ? (
        <img src={docURL.thumb(doc)} alt="" loading="lazy" onError={() => setBroken(true)} />
      ) : (
        <span class="thumb-stand-in">
          {busy ? (
            <Loader size={18} class="spin" />
          ) : doc.status === "failed" ? (
            <TriangleAlert size={18} />
          ) : (
            <FileText size={18} />
          )}
        </span>
      )}
    </div>
  );
}

export function StatusChip(props: { doc: Doc }) {
  switch (props.doc.status) {
    case "pending":
    case "processing":
      return <span class="chip chip-accent">Processing</span>;
    case "failed":
      return <span class="chip chip-bad">Failed</span>;
  }
  if (props.doc.warning) {
    return (
      <span class="chip chip-warn" title={props.doc.warning}>
        Partly read
      </span>
    );
  }
  return null;
}

export function ExpiryChip(props: { expires: string }) {
  if (!props.expires) return null;
  const e = expiryText(props.expires);
  if (!e.tone) return null;
  return <span class={`chip chip-${e.tone}`}>{e.text}</span>;
}

export function FamilyMark(props: { doc: Doc }) {
  if (!props.doc.family) return null;
  return (
    <span class="family-mark" title="In the Family space">
      <Users size={12} /> Family
    </span>
  );
}

export function Snippet(props: { text?: string }) {
  if (!props.text) return null;
  return (
    <span class="snippet">
      {snippetParts(props.text).map((p, i) => (p.match ? <mark key={i}>{p.text}</mark> : p.text))}
    </span>
  );
}

export function CategorySelect(props: {
  value: string;
  categories: Category[];
  onChange: (v: string) => void;
  any?: string;
  inbox?: string;
  id?: string;
}) {
  return (
    <select
      id={props.id}
      class="input select"
      value={props.value}
      onChange={(e) => props.onChange(e.currentTarget.value)}
    >
      {props.any !== undefined && <option value="">{props.any}</option>}
      <option value={props.any !== undefined ? "none" : "0"}>{props.inbox ?? "Inbox"}</option>
      {props.categories.map((c) => (
        <option key={c.id} value={String(c.id)}>
          {c.name}
        </option>
      ))}
    </select>
  );
}

/** What the file pickers take. */
export const ACCEPT = "application/pdf,image/jpeg,image/png,image/heic,image/heif,.pdf,.heic,.heif";

/** Only me or the family, as two big buttons. */
export function SpaceToggle(props: {
  family: boolean;
  onChange: (family: boolean) => void;
  label?: ComponentChildren;
}) {
  const options: [boolean, string, string][] = [
    [false, "Only me", "Private"],
    [true, "The family", "Everyone here"],
  ];
  return (
    <div class="field">
      <span class="field-label">{props.label ?? "Who can see it?"}</span>
      <div class="space-toggle" role="radiogroup" aria-label="Who can see it">
        {options.map(([family, label, sub]) => (
          <button
            key={label}
            type="button"
            role="radio"
            aria-checked={props.family === family}
            class={props.family === family ? "active" : ""}
            onClick={() => props.onChange(family)}
          >
            {label}
            <small>{sub}</small>
          </button>
        ))}
      </div>
    </div>
  );
}

const STATUS_TEXT = {
  added: "Saved",
  duplicate: "Already there",
  skipped: "Skipped",
  failed: "Failed",
};

export function ResultList(props: { results: IngestResult[] }) {
  return (
    <div class="list">
      {props.results.map((r, i) => (
        <div key={i} class="list-row">
          <span
            class={`dot ${r.status === "added" ? "good" : r.status === "duplicate" ? "" : "bad"}`}
          />
          <span class="list-main list-link">
            {r.document && r.status !== "failed" ? (
              <a class="list-title link" href={`/documents/${r.document.id}`}>
                {r.name}
              </a>
            ) : (
              <span class="list-title">{r.name}</span>
            )}
            <span class="list-sub">
              {STATUS_TEXT[r.status]}
              {r.message && r.status !== "added" ? `: ${r.message}` : ""}
            </span>
          </span>
        </div>
      ))}
    </div>
  );
}

const CATEGORY_ICONS: [RegExp, LucideIcon][] = [
  [/^id\b|identity/i, IdCard],
  [/propert|house|home/i, House],
  [/medic|health/i, HeartPulse],
  [/insur/i, Shield],
  [/tax/i, Receipt],
  [/bill/i, ReceiptText],
  [/vehicle|car\b/i, Car],
  [/educat|school/i, GraduationCap],
  [/bank|invest|financ/i, Landmark],
  [/travel/i, Plane],
  [/work|job/i, Briefcase],
];

/** An icon for a category, by its name; the inbox has its own. */
export function CategoryIcon(props: { name: string; inbox?: boolean; size?: number }) {
  const Icon = props.inbox
    ? Inbox
    : (CATEGORY_ICONS.find(([re]) => re.test(props.name))?.[1] ?? Folder);
  return <Icon size={props.size ?? 22} aria-hidden="true" />;
}

/** The category (or Not sorted yet) in its own colour, with its icon. */
export function CategoryTag(props: { name: string; class?: string }) {
  return (
    <span class={`cat-tag ${props.class ?? ""}`} style={{ "--cat": categoryColor(props.name) }}>
      <CategoryIcon name={props.name} inbox={!props.name} size={15} />
      {props.name || "Not sorted yet"}
    </span>
  );
}

/**
 * Documents as rows: a small page one, a big title, what and when. While
 * selecting (selected set), a tap selects instead of opening.
 */
export function DocRows(props: {
  docs: Doc[];
  selected?: Set<number> | null;
  onToggle?: (id: number) => void;
  class?: string;
}) {
  const { selected } = props;
  return (
    <div class={`doc-rows ${props.class ?? ""}`}>
      {props.docs.map((d) => {
        const body = (
          <>
            {selected && (
              <span class="select-box" aria-hidden="true">
                {selected.has(d.id) && <Check size={16} />}
              </span>
            )}
            <DocThumb doc={d} class="thumb-row" />
            <span class="doc-row-main">
              <span class="doc-row-title">{d.title}</span>
              <span class="doc-row-meta">
                <CategoryTag name={d.category} />
                {d.doc_date && <span>{formatDate(d.doc_date)}</span>}
              </span>
              <span class="chips">
                <StatusChip doc={d} />
                {d.suggestion && <span class="chip chip-accent">Suggestion</span>}
                <ExpiryChip expires={d.expires} />
                <FamilyMark doc={d} />
              </span>
              <Snippet text={d.snippet} />
            </span>
            {!selected && <ChevronRight size={22} class="doc-row-go" />}
          </>
        );
        return selected ? (
          <button
            key={d.id}
            type="button"
            class={`doc-row ${selected.has(d.id) ? "selected" : ""}`}
            aria-pressed={selected.has(d.id)}
            onClick={() => props.onToggle?.(d.id)}
          >
            {body}
          </button>
        ) : (
          <a key={d.id} class="doc-row" href={`/documents/${d.id}`}>
            {body}
          </a>
        );
      })}
    </div>
  );
}
