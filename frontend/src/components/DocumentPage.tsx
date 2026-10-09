import {
  ArrowLeft,
  Download,
  Eye,
  FileText,
  Info,
  Pencil,
  RefreshCw,
  Share2,
  Sparkles,
  Trash2,
} from "lucide-preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { api, docURL } from "../api";
import { useData, useUnsavedWarning } from "../hooks";
import { ago, bytes, expiryText, formatDate, langName, plural } from "../lib";
import { goBack, navigate } from "../router";
import type { Category, Doc, Settings, User } from "../types";
import { CategorySelect, CategoryTag, DocThumb, SpaceToggle } from "./docs";
import { Disclosure, ErrorNote, Field, useAction } from "./ui";

/**
 * One document, read first: what it is in plain words and three big
 * actions. Changing it, its text and the file's details open on purpose.
 */
export function DocumentPage(props: { id: number; user: User }) {
  const doc = useData(() => api.document(props.id), 0, [props.id]);
  const cats = useData(api.categories);
  const settings = useData(api.settings);
  const [open, setOpen] = useState("");
  const d = doc.data;
  const admin = props.user.admin;
  const toggle = (name: string) => setOpen(open === name ? "" : name);

  // Follow processing until it's done.
  const busy = d?.status === "pending" || d?.status === "processing";
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(doc.reload, 2500);
    return () => clearInterval(t);
  }, [busy]);

  if (doc.error) {
    return (
      <div class="page">
        <BackLink />
        <ErrorNote>{doc.error}</ErrorNote>
      </div>
    );
  }
  if (!d) return <div class="page loading loading-page" />;
  const showClassifier =
    admin &&
    (!!settings.data?.classifier || !!d.classified || !!d.classify_error) &&
    d.status !== "pending" &&
    d.status !== "processing";

  return (
    <div class="page doc-page">
      <header class="page-head">
        <BackLink />
        <h1 class="page-title page-title-small doc-title">{d.title}</h1>
      </header>

      <div class="doc-top">
        <a
          class="doc-preview"
          href={docURL.file(d.id)}
          target="_blank"
          rel="noopener"
          aria-label="Open the document"
        >
          <DocThumb doc={d} class="thumb-large" />
        </a>
        <Facts doc={d} />
        <DocActions doc={d} />
      </div>

      <ProcessingNote doc={d} admin={admin} onQueued={doc.setData} />
      {admin && d.suggestion && <SuggestionNote doc={d} onDone={doc.setData} />}

      <div class="disclosures">
        <Disclosure
          icon={<Pencil size={22} />}
          title="Change name, type or dates"
          open={open === "edit"}
          onToggle={() => toggle("edit")}
        >
          <EditForm
            key={d.updated}
            doc={d}
            categories={cats.data ?? []}
            onSaved={(saved) => {
              doc.setData(saved);
              setOpen("");
            }}
            onCancel={() => setOpen("")}
          />
        </Disclosure>
        {!busy && (
          <Disclosure
            icon={<FileText size={22} />}
            title="Show the words in it"
            open={open === "text"}
            onToggle={() => toggle("text")}
          >
            <TextSection doc={d} settings={settings.data} onQueued={doc.setData} />
          </Disclosure>
        )}
        {showClassifier && (
          <Disclosure
            icon={<Sparkles size={22} />}
            title="Suggestions"
            open={open === "suggest"}
            onToggle={() => toggle("suggest")}
          >
            <ClassifierSection doc={d} settings={settings.data} onQueued={doc.setData} />
          </Disclosure>
        )}
        <Disclosure
          icon={<Info size={22} />}
          title="File details"
          open={open === "file"}
          onToggle={() => toggle("file")}
        >
          <FileSection doc={d} admin={admin} />
        </Disclosure>
        <DeleteRow doc={d} />
      </div>
    </div>
  );
}

function BackLink() {
  return (
    <a
      class="back"
      href="/"
      onClick={(e) => {
        e.preventDefault();
        goBack("/");
      }}
    >
      <ArrowLeft size={20} /> Back
    </a>
  );
}

/** What it is, in a few plain lines. */
function Facts(props: { doc: Doc }) {
  const d = props.doc;
  const expiry = d.expires ? expiryText(d.expires) : null;
  return (
    <dl class="facts">
      <div>
        <dt>Type</dt>
        <dd>
          <CategoryTag name={d.category} class="cat-tag-big" />
        </dd>
      </div>
      {d.doc_date && (
        <div>
          <dt>Date</dt>
          <dd>{formatDate(d.doc_date)}</dd>
        </div>
      )}
      {d.expires && expiry && (
        <div>
          <dt>Expires</dt>
          <dd class={expiry.tone ? `tone-${expiry.tone}` : ""}>
            {formatDate(d.expires)}
            {expiry.tone && <span class="facts-sub">{expiry.text}</span>}
          </dd>
        </div>
      )}
      <div>
        <dt>Who can see it</dt>
        <dd>{d.family ? "The family" : "Only you"}</dd>
      </div>
      {d.tags.length > 0 && (
        <div>
          <dt>Tags</dt>
          <dd>{d.tags.join(", ")}</dd>
        </div>
      )}
      {d.notes && (
        <div>
          <dt>Notes</dt>
          <dd class="facts-notes">{d.notes}</dd>
        </div>
      )}
    </dl>
  );
}

/** Open, share (the share sheet on iPhone) and save a copy. */
function DocActions(props: { doc: Doc }) {
  const d = props.doc;
  const name = `${d.title.replace(/[/\\:*?"<>|]/g, "_")}.pdf`;
  const shareable = typeof navigator.share === "function";
  // iOS only opens the share sheet straight from a tap, so the file is
  // fetched beforehand.
  const blob = useRef<Promise<Blob> | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    if (shareable && d.status === "ready" && d.size < 40 << 20) {
      blob.current = fetch(docURL.file(d.id)).then((r) => r.blob());
      blob.current.catch(() => {});
    }
  }, [d.id, d.status]);

  const share = async () => {
    setError("");
    try {
      const b = await (blob.current ?? fetch(docURL.file(d.id)).then((r) => r.blob()));
      const file = new File([b], b.type === "application/pdf" ? name : d.file_name, {
        type: b.type,
      });
      if (navigator.canShare?.({ files: [file] })) {
        await navigator.share({ files: [file], title: d.title });
      } else {
        window.location.href = docURL.file(d.id, true);
      }
    } catch (e) {
      if ((e as Error).name !== "AbortError") setError((e as Error).message);
    }
  };

  return (
    <div class="doc-actions">
      <a
        class="btn btn-primary btn-big doc-open"
        href={docURL.file(d.id)}
        target="_blank"
        rel="noopener"
      >
        <Eye size={20} /> Open document
      </a>
      {shareable && (
        <button class="btn btn-big" onClick={share}>
          <Share2 size={20} /> Send / Share
        </button>
      )}
      <a class="btn btn-big" href={docURL.file(d.id, true)} download>
        <Download size={20} /> Save a copy
      </a>
      {error && <div class="form-error">{error}</div>}
    </div>
  );
}

function SuggestionNote(props: { doc: Doc; onDone: (d: Doc) => void }) {
  const { doc } = props;
  const s = doc.suggestion!;
  const { busy, error, run } = useAction();
  const rows: [string, string][] = [];
  if (s.category && s.category !== doc.category) rows.push(["Type", s.category]);
  if (s.tags?.length) rows.push(["Tags", s.tags.join(", ")]);
  if (s.new_tags?.length)
    rows.push(["New tags", `${s.new_tags.join(", ")} (not on your tag list)`]);
  if (s.doc_date && s.doc_date !== doc.doc_date) rows.push(["Date", formatDate(s.doc_date)]);
  if (s.expires && s.expires !== doc.expires) rows.push(["Expires", formatDate(s.expires)]);
  return (
    <div class="note note-accent suggestion">
      <div class="eyebrow eyebrow-accent">
        <Sparkles size={12} /> Suggested
      </div>
      <dl class="kv">
        {rows.map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
      {error && <div class="form-error">{error}</div>}
      <div class="toolbar">
        <button
          class="btn btn-primary btn-small"
          disabled={busy}
          onClick={() => run(async () => props.onDone(await api.applySuggestion(doc.id)))}
        >
          Apply
        </button>
        <button
          class="btn btn-ghost btn-small"
          disabled={busy}
          onClick={() =>
            run(async () => {
              await api.dismissSuggestion(doc.id);
              props.onDone({ ...doc, suggestion: undefined });
            })
          }
        >
          Dismiss
        </button>
      </div>
    </div>
  );
}

function ProcessingNote(props: { doc: Doc; admin: boolean; onQueued: (d: Doc) => void }) {
  const { doc } = props;
  const { busy, error, run } = useAction();
  if (doc.status === "pending" || doc.status === "processing") {
    return (
      <div class="note note-accent">
        <span class="dot accent" /> Reading the document so it can be searched. Long scans take a
        minute or two; you can leave this page.
      </div>
    );
  }
  if (doc.status !== "failed") {
    if (!doc.warning) return null;
    return (
      <div class="note note-warn">
        <strong>Some pages couldn't be read,</strong> so searching may miss words on them. The
        document itself is fine.
        {props.admin && <div class="muted note-detail">{doc.warning}</div>}
      </div>
    );
  }
  return (
    <div class="note note-bad">
      <strong>This document couldn't be read.</strong> It's saved, but its picture and words aren't
      ready.
      {doc.error && <div class="muted note-detail">{doc.error}</div>}
      {error && <div class="form-error">{error}</div>}
      <div class="toolbar note-actions">
        <button
          class="btn"
          disabled={busy}
          onClick={() => run(async () => props.onQueued(await api.reprocess(doc.id, false)))}
        >
          <RefreshCw size={16} /> Try again
        </button>
      </div>
    </div>
  );
}

function EditForm(props: {
  doc: Doc;
  categories: Category[];
  onSaved: (d: Doc) => void;
  onCancel: () => void;
}) {
  const [d, setD] = useState(props.doc);
  const [tags, setTags] = useState(props.doc.tags.join(", "));
  const { busy, error, run } = useAction();
  const edited: Doc = {
    ...d,
    tags: tags
      .split(",")
      .map((t) => t.trim())
      .filter(Boolean),
  };
  const dirty =
    JSON.stringify([edited.title, edited.category_id, edited.doc_date, edited.expires]) !==
      JSON.stringify([
        props.doc.title,
        props.doc.category_id,
        props.doc.doc_date,
        props.doc.expires,
      ]) ||
    edited.notes !== props.doc.notes ||
    edited.family !== props.doc.family ||
    edited.tags.join(",") !== props.doc.tags.join(",");
  useUnsavedWarning(dirty);

  const save = (e: Event) => {
    e.preventDefault();
    if (edited.family !== props.doc.family && !edited.family && props.doc.family) {
      if (!confirm("Make it private? The rest of the family won't see it any more.")) {
        return;
      }
    }
    run(async () => props.onSaved(await api.updateDocument(edited)));
  };
  const expiry = d.expires ? expiryText(d.expires) : null;

  return (
    <form class="form doc-form" onSubmit={save}>
      <Field label="Name">
        <input
          class="input"
          value={d.title}
          onInput={(e) => setD({ ...d, title: e.currentTarget.value })}
        />
      </Field>
      <Field label="Type">
        <CategorySelect
          value={String(d.category_id)}
          categories={props.categories}
          onChange={(v) => setD({ ...d, category_id: Number(v) })}
          inbox="Not sorted yet"
        />
      </Field>
      <div class="form-grid">
        <Field label="Date on the document">
          <input
            class="input"
            type="date"
            value={d.doc_date}
            onInput={(e) => setD({ ...d, doc_date: e.currentTarget.value })}
          />
        </Field>
        <Field
          label="Expires on"
          hint={
            expiry ? (
              <span class={`tone-${expiry.tone}`}>{expiry.text}</span>
            ) : (
              "For a passport, policy, licence…"
            )
          }
        >
          <input
            class="input"
            type="date"
            value={d.expires}
            onInput={(e) => setD({ ...d, expires: e.currentTarget.value })}
          />
        </Field>
      </div>
      <Field label="Tags" hint="Words to find it by, with commas between them">
        <input class="input" value={tags} onInput={(e) => setTags(e.currentTarget.value)} />
      </Field>
      <Field label="Notes">
        <textarea
          class="input textarea"
          rows={3}
          value={d.notes}
          onInput={(e) => setD({ ...d, notes: e.currentTarget.value })}
        />
      </Field>
      <SpaceToggle family={d.family} onChange={(family) => setD({ ...d, family })} />
      {error && <div class="form-error">{error}</div>}
      <div class="add-buttons">
        <button class="btn btn-primary btn-big" disabled={!dirty || busy || !d.title.trim()}>
          Save changes
        </button>
        <button
          type="button"
          class="btn btn-big"
          onClick={() => {
            setD(props.doc);
            setTags(props.doc.tags.join(", "));
            props.onCancel();
          }}
        >
          Cancel
        </button>
      </div>
    </form>
  );
}

function TextSection(props: { doc: Doc; settings: Settings | null; onQueued: (d: Doc) => void }) {
  const { doc, settings } = props;
  const text = useData(() => api.documentText(doc.id), 0, [doc.id, doc.updated, doc.status]);
  const [lang, setLang] = useState(doc.ocr_lang || settings?.ocr_langs || "eng+hin");
  const { busy, error, run } = useAction();
  const t = text.data?.text ?? "";
  const langs = settings?.languages.length ? settings.languages : ["eng", "hin"];
  const options = Array.from(new Set([...langs, langs.join("+"), lang])).filter(Boolean);

  return (
    <div class="section">
      <p class="muted">
        {doc.text_source === "pdf" && "These are the words search looks through."}
        {doc.text_source === "ocr" &&
          `Read from the picture (${langName(doc.ocr_lang || "")}). These are the words search looks through.`}
        {!doc.text_source && "No words found in it yet."}
      </p>
      {t ? (
        <div class="doc-text open">
          <pre>{t}</pre>
        </div>
      ) : (
        <p class="muted">{text.data ? "" : text.error || "Loading…"}</p>
      )}
      <p class="muted">Words wrong or missing? Read it again, in these languages:</p>
      <div class="toolbar">
        <select
          class="input select select-auto"
          value={lang}
          aria-label="Languages to read"
          onChange={(e) => setLang(e.currentTarget.value)}
        >
          {options.map((l) => (
            <option key={l} value={l}>
              {langName(l)}
            </option>
          ))}
        </select>
        <button
          class="btn"
          disabled={busy}
          onClick={() => run(async () => props.onQueued(await api.reprocess(doc.id, true, lang)))}
        >
          <RefreshCw size={16} /> Read the text again
        </button>
        {error && <span class="form-error">{error}</span>}
      </div>
    </div>
  );
}

/** The classifier: when it last looked, asking again, and exactly what it was sent. */
function ClassifierSection(props: {
  doc: Doc;
  settings: Settings | null;
  onQueued: (d: Doc) => void;
}) {
  const { doc, settings } = props;
  const [shown, setShown] = useState<string | null>(null);
  const { busy, error, run } = useAction();
  const on = !!settings?.classifier;
  return (
    <div class="section">
      {doc.classify_error && (
        <div class="note note-warn">Couldn't get a suggestion: {doc.classify_error}</div>
      )}
      <p class="muted">
        {settings?.classifier.startsWith("llm:")
          ? `${settings.classifier.slice(4)}. `
          : on
            ? "Classifier. "
            : "Off. "}
        {doc.classified
          ? `Last asked ${ago(doc.classified)}${doc.suggestion ? "; the suggestion is above." : "; nothing to suggest, or it was applied."}`
          : "Not asked yet."}{" "}
        It only ever gets the masked text, never the file.
      </p>
      {shown !== null && (
        <div class="doc-text open">
          <pre>{shown || "Nothing sent yet."}</pre>
        </div>
      )}
      <div class="toolbar">
        {on && (
          <button
            class="btn btn-small"
            disabled={busy}
            onClick={() =>
              run(async () => {
                await api.requestSuggestions({}, [doc.id]);
                props.onQueued(await api.document(doc.id));
              })
            }
          >
            <Sparkles size={13} /> {doc.classified ? "Suggest again" : "Get a suggestion"}
          </button>
        )}
        <button
          class="btn btn-ghost btn-small"
          disabled={busy}
          onClick={() =>
            shown !== null
              ? setShown(null)
              : run(async () => setShown((await api.classifierInput(doc.id)).text))
          }
        >
          {shown !== null ? "Hide what it saw" : "What the classifier saw"}
        </button>
        {error && <span class="form-error">{error}</span>}
      </div>
    </div>
  );
}

function FileSection(props: { doc: Doc; admin: boolean }) {
  const d = props.doc;
  const photo = d.mime.startsWith("image/");
  return (
    <div class="section">
      <dl class="kv kv-wide">
        <div>
          <dt>Added</dt>
          <dd>
            {formatDate(d.created.slice(0, 10))} {d.added_by && `by ${d.added_by}`}
          </dd>
        </div>
        <div>
          <dt>Came in as</dt>
          <dd>{d.file_name}</dd>
        </div>
        <div>
          <dt>Kind</dt>
          <dd>
            {photo ? "A photo, shown as a PDF" : "PDF"}
            {d.pages > 0 && ` · ${plural(d.pages, "page")}`} · {bytes(d.size)}
          </dd>
        </div>
        {props.admin && (
          <div>
            <dt>SHA-256</dt>
            <dd class="mono hash">{d.sha256}</dd>
          </div>
        )}
      </dl>
      <div class="toolbar">
        <a class="btn" href={docURL.original(d.id)} download>
          <Download size={16} /> {photo ? "Save the original photo" : "Save the original file"}
        </a>
      </div>
    </div>
  );
}

function DeleteRow(props: { doc: Doc }) {
  const d = props.doc;
  const { busy, error, run } = useAction();
  const remove = () =>
    run(async () => {
      if (!confirm(`Delete “${d.title}”? This can't be undone.`)) return;
      await api.deleteDocument(d.id);
      navigate("/", true);
    });
  return (
    <>
      <Disclosure
        icon={<Trash2 size={22} />}
        title={busy ? "Deleting…" : "Delete this document"}
        open={false}
        onToggle={remove}
        danger
      />
      {error && <ErrorNote>{error}</ErrorNote>}
    </>
  );
}
