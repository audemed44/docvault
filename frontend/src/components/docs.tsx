import { FileText, Loader, TriangleAlert, Users } from "lucide-preact";
import { useState } from "preact/hooks";
import { docURL } from "../api";
import { expiryText, formatDate, snippetParts } from "../lib";
import type { Category, Doc } from "../types";

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

/** Category (or Inbox), date and space, in one line. */
export function docMeta(d: Doc): string {
  return [d.category || "Inbox", formatDate(d.doc_date)].join(" · ");
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
